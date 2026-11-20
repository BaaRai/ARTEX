#!/usr/bin/env bash
# egress-guard entrypoint.
#   EGRESS_MODE   monitor (default: log only) | enforce (log + reject) | off (no rules, DNS only)
#   ARTEX_SUBNET  the ARTEX docker network (must match docker-compose.yml)
#   ARTEX_GATEWAY bridge gateway IP; dnsmasq listens here
#   UPSTREAM_DNS  comma-separated upstream resolvers; empty = host /etc/resolv.conf
#   IPSET_TIMEOUT seconds a resolved IP stays allowed after its last lookup (default 21600)
set -euo pipefail

MODE="${EGRESS_MODE:-monitor}"
SUBNET="${ARTEX_SUBNET:?ARTEX_SUBNET required}"
GW="${ARTEX_GATEWAY:?ARTEX_GATEWAY required}"
TIMEOUT="${IPSET_TIMEOUT:-21600}"
LISTS=/etc/egress-guard
LOGDIR=/var/log/egress
CONF=/run/egress-guard/dnsmasq.conf
IPT=iptables
command -v iptables-nft >/dev/null 2>&1 && IPT=iptables-nft
[ -n "${IPTABLES_BIN:-}" ] && IPT="$IPTABLES_BIN"

log() { echo "[egress-guard] $*"; }

case "$MODE" in monitor|enforce|off) ;; *) log "invalid EGRESS_MODE=$MODE"; exit 1;; esac

# Read a list file: strip comments/blank lines, lowercase, drop leading dots/wildcards.
read_list() { [ -f "$1" ] || return 0; sed -e 's/#.*//' -e 's/[[:space:]]//g' "$1" | tr 'A-Z' 'a-z' | sed -e 's/^\*\.//' -e 's/^\.//' | grep -v '^$' || true; }

cleanup_rules() {
  # filter table: detach and delete our chain
  while $IPT -C DOCKER-USER -s "$SUBNET" ! -d "$SUBNET" -j ARTEX-EGRESS 2>/dev/null; do
    $IPT -D DOCKER-USER -s "$SUBNET" ! -d "$SUBNET" -j ARTEX-EGRESS
  done
  $IPT -F ARTEX-EGRESS 2>/dev/null || true
  $IPT -X ARTEX-EGRESS 2>/dev/null || true
  # nat table: DNS redirect
  while $IPT -t nat -C PREROUTING -s "$SUBNET" -j ARTEX-DNS 2>/dev/null; do
    $IPT -t nat -D PREROUTING -s "$SUBNET" -j ARTEX-DNS
  done
  $IPT -t nat -F ARTEX-DNS 2>/dev/null || true
  $IPT -t nat -X ARTEX-DNS 2>/dev/null || true
}

apply_rules() {
  ipset create artex_allow hash:ip timeout "$TIMEOUT" -exist
  ipset create artex_scope hash:net -exist
  ipset flush artex_scope
  local n=0
  while read -r cidr; do ipset add artex_scope "$cidr" -exist && n=$((n+1)); done < <(read_list "$LISTS/scope-cidrs.txt")
  log "scope CIDRs loaded: $n"

  [ "$MODE" = off ] && { log "mode off: no firewall rules"; return; }

  # DOCKER-USER exists whenever dockerd runs; create it only for standalone tests.
  if ! $IPT -L DOCKER-USER -n >/dev/null 2>&1; then
    $IPT -N DOCKER-USER; $IPT -I FORWARD -j DOCKER-USER
  fi

  # Redirect every DNS query leaving the ARTEX network to dnsmasq, so tools with
  # hard-coded resolvers (dnsx, bbot, dig @8.8.8.8) still populate the allowlist.
  $IPT -t nat -N ARTEX-DNS
  $IPT -t nat -A ARTEX-DNS -d "$GW" -j RETURN
  $IPT -t nat -A ARTEX-DNS -p udp --dport 53 -j DNAT --to-destination "$GW:53"
  $IPT -t nat -A ARTEX-DNS -p tcp --dport 53 -j DNAT --to-destination "$GW:53"
  $IPT -t nat -I PREROUTING 1 -s "$SUBNET" -j ARTEX-DNS

  $IPT -N ARTEX-EGRESS
  $IPT -A ARTEX-EGRESS -m conntrack --ctstate ESTABLISHED,RELATED -j RETURN
  $IPT -A ARTEX-EGRESS -m set --match-set artex_allow dst -j RETURN
  $IPT -A ARTEX-EGRESS -m set --match-set artex_scope dst -j RETURN
  $IPT -A ARTEX-EGRESS -m limit --limit 30/min --limit-burst 60 -j LOG --log-prefix "ARTEX-EGRESS-$MODE " --log-level 4
  if [ "$MODE" = enforce ]; then
    $IPT -A ARTEX-EGRESS -p tcp -j REJECT --reject-with tcp-reset
    $IPT -A ARTEX-EGRESS -j REJECT --reject-with icmp-port-unreachable
  else
    $IPT -A ARTEX-EGRESS -j RETURN
  fi
  $IPT -I DOCKER-USER 1 -s "$SUBNET" ! -d "$SUBNET" -j ARTEX-EGRESS
  log "firewall rules applied (mode=$MODE, subnet=$SUBNET)"
}

write_dnsmasq_conf() {
  mkdir -p "$(dirname "$CONF")" "$LOGDIR"
  {
    echo "listen-address=$GW"
    echo "bind-dynamic"
    echo "except-interface=lo"
    echo "no-hosts"
    echo "cache-size=10000"
    echo "log-queries=extra"
    echo "log-facility=$LOGDIR/dnsmasq.log"
    if [ -n "${UPSTREAM_DNS:-}" ]; then
      echo "no-resolv"
      IFS=',' read -ra ups <<< "$UPSTREAM_DNS"
      for u in "${ups[@]}"; do echo "server=$u"; done
    fi
  } > "$CONF"
  local n=0
  while read -r d; do echo "ipset=/$d/artex_allow" >> "$CONF"; n=$((n+1)); done < <(read_list "$LISTS/base-domains.txt"; read_list "$LISTS/scope-domains.txt")
  log "allowed domain suffixes: $n"
}

on_term() { log "stopping"; [ -n "${DNSMASQ_PID:-}" ] && kill "$DNSMASQ_PID" 2>/dev/null || true; cleanup_rules; exit 0; }
trap on_term TERM INT

cleanup_rules
apply_rules
write_dnsmasq_conf
dnsmasq --test -C "$CONF"
dnsmasq -k -C "$CONF" &
DNSMASQ_PID=$!
log "dnsmasq listening on $GW:53 (pid $DNSMASQ_PID)"
wait "$DNSMASQ_PID"
# dnsmasq died unexpectedly: leave rules in place (fail closed) and let docker restart us.
log "dnsmasq exited; rules left in place"
exit 1
