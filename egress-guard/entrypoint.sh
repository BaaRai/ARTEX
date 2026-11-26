#!/usr/bin/env bash
# egress-guard entrypoint.
#   EGRESS_MODE           monitor (default: log only) | enforce (log + reject) | off (DNS only)
#   ARTEX_SUBNET          the ARTEX docker network (must match docker-compose.yml)
#   ARTEX_GATEWAY         bridge gateway IP; dnsmasq listens here
#   ARTEX_SUBNET6         optional IPv6 subnet; when set, enforce also drops its egress
#   EGRESS_FILTER_GATEWAY 1 = in enforce, also drop non-DNS traffic from the subnet to the
#                         host/gateway (closes the host-proxy bypass). Off by default because
#                         it edits the host INPUT chain. See README "Known gaps".
#   UPSTREAM_DNS          comma-separated upstream resolvers; empty = host /etc/resolv.conf
#   IPSET_TIMEOUT         seconds a resolved IP stays allowed after its last lookup (default 21600)
#
# Fail-closed (enforce): a blanket deny for the subnet's egress is installed BEFORE the allowlist
# is built and removed only after setup completes. If any setup step fails, the ERR trap leaves
# that deny in place and exits non-zero, so Docker restarts the guard while traffic stays blocked.
# The operator then sees the ARTEX container lose outbound connectivity (except DNS) until the
# guard comes up cleanly. In monitor/off mode a setup failure is non-fatal by design (log only).
set -Eeuo pipefail   # -E so the ERR trap fires for failures inside functions (apply_rules)

MODE="${EGRESS_MODE:-monitor}"
SUBNET="${ARTEX_SUBNET:?ARTEX_SUBNET required}"
GW="${ARTEX_GATEWAY:?ARTEX_GATEWAY required}"
SUBNET6="${ARTEX_SUBNET6:-}"
FILTER_GW="${EGRESS_FILTER_GATEWAY:-0}"
TIMEOUT="${IPSET_TIMEOUT:-21600}"
LISTS=/etc/egress-guard
LOGDIR=/var/log/egress
CONF=/run/egress-guard/dnsmasq.conf
IPT=iptables
command -v iptables-nft >/dev/null 2>&1 && IPT=iptables-nft
[ -n "${IPTABLES_BIN:-}" ] && IPT="$IPTABLES_BIN"
IP6T=ip6tables
command -v ip6tables-nft >/dev/null 2>&1 && IP6T=ip6tables-nft
[ -n "${IP6TABLES_BIN:-}" ] && IP6T="$IP6TABLES_BIN"

BOOT_CMT="artex-egress-bootstrap-deny"
V6_CMT="artex-egress-v6-deny"

log() { echo "[egress-guard] $*"; }

case "$MODE" in monitor|enforce|off) ;; *) log "invalid EGRESS_MODE=$MODE"; exit 1;; esac

# Read a list file: strip comments/blank lines, lowercase, drop leading dots/wildcards.
read_list() { [ -f "$1" ] || return 0; sed -e 's/#.*//' -e 's/[[:space:]]//g' "$1" | tr 'A-Z' 'a-z' | sed -e 's/^\*\.//' -e 's/^\.//' | grep -v '^$' || true; }

ensure_docker_user() {
  # DOCKER-USER exists whenever dockerd runs; create it only for standalone tests.
  $IPT -L DOCKER-USER -n >/dev/null 2>&1 || { $IPT -N DOCKER-USER; $IPT -I FORWARD -j DOCKER-USER; }
  if [ -n "$SUBNET6" ] && command -v "$IP6T" >/dev/null 2>&1; then
    $IP6T -L DOCKER-USER -n >/dev/null 2>&1 || { $IP6T -N DOCKER-USER; $IP6T -I FORWARD -j DOCKER-USER; }
  fi
}

bootstrap_deny() {
  # enforce only: block the subnet's egress up-front so a partial setup fails closed.
  [ "$MODE" = enforce ] || return 0
  $IPT -I DOCKER-USER 1 -s "$SUBNET" ! -d "$SUBNET" -m comment --comment "$BOOT_CMT" -j DROP
  if [ -n "$SUBNET6" ] && command -v "$IP6T" >/dev/null 2>&1; then
    $IP6T -I DOCKER-USER 1 -s "$SUBNET6" -m comment --comment "$BOOT_CMT" -j DROP 2>/dev/null \
      || log "WARN: ip6tables unavailable; IPv6 egress NOT filtered"
  elif command -v "$IP6T" >/dev/null 2>&1; then
    log "WARN: ARTEX_SUBNET6 unset; IPv6 egress is NOT filtered (keep the ARTEX network IPv4-only)"
  fi
}

remove_bootstrap_deny() {
  [ "$MODE" = enforce ] || return 0
  while $IPT -C DOCKER-USER -s "$SUBNET" ! -d "$SUBNET" -m comment --comment "$BOOT_CMT" -j DROP 2>/dev/null; do
    $IPT -D DOCKER-USER -s "$SUBNET" ! -d "$SUBNET" -m comment --comment "$BOOT_CMT" -j DROP
  done
  [ -n "$SUBNET6" ] && command -v "$IP6T" >/dev/null 2>&1 && \
    while $IP6T -C DOCKER-USER -s "$SUBNET6" -m comment --comment "$BOOT_CMT" -j DROP 2>/dev/null; do
      $IP6T -D DOCKER-USER -s "$SUBNET6" -m comment --comment "$BOOT_CMT" -j DROP
    done
  return 0
}

cleanup_rules() {
  # Leave any bootstrap deny untouched here: cleanup runs on stop (on_term), where removing it
  # is correct, and on startup before setup, where re-adding happens in bootstrap_deny.
  remove_bootstrap_deny
  # filter table: detach and delete our chain
  while $IPT -C DOCKER-USER -s "$SUBNET" ! -d "$SUBNET" -j ARTEX-EGRESS 2>/dev/null; do
    $IPT -D DOCKER-USER -s "$SUBNET" ! -d "$SUBNET" -j ARTEX-EGRESS
  done
  $IPT -F ARTEX-EGRESS 2>/dev/null || true
  $IPT -X ARTEX-EGRESS 2>/dev/null || true
  # optional host INPUT filter for the gateway
  while $IPT -C INPUT -s "$SUBNET" -d "$GW" -j ARTEX-EGRESS-HOST 2>/dev/null; do
    $IPT -D INPUT -s "$SUBNET" -d "$GW" -j ARTEX-EGRESS-HOST
  done
  $IPT -F ARTEX-EGRESS-HOST 2>/dev/null || true
  $IPT -X ARTEX-EGRESS-HOST 2>/dev/null || true
  # nat table: DNS redirect
  while $IPT -t nat -C PREROUTING -s "$SUBNET" -j ARTEX-DNS 2>/dev/null; do
    $IPT -t nat -D PREROUTING -s "$SUBNET" -j ARTEX-DNS
  done
  $IPT -t nat -F ARTEX-DNS 2>/dev/null || true
  $IPT -t nat -X ARTEX-DNS 2>/dev/null || true
  # persistent IPv6 deny rule installed in apply_rules
  if [ -n "$SUBNET6" ] && command -v "$IP6T" >/dev/null 2>&1; then
    while $IP6T -C DOCKER-USER -s "$SUBNET6" -m comment --comment "$V6_CMT" -j DROP 2>/dev/null; do
      $IP6T -D DOCKER-USER -s "$SUBNET6" -m comment --comment "$V6_CMT" -j DROP
    done
  fi
}

apply_rules() {
  ipset create artex_allow hash:ip timeout "$TIMEOUT" -exist
  ipset flush artex_allow   # drop IPs allowed in a previous run: a host removed from the
                            # lists must not stay reachable until the ipset timeout expires.
  ipset create artex_scope hash:net -exist
  ipset flush artex_scope
  local n=0
  while read -r cidr; do ipset add artex_scope "$cidr" -exist && n=$((n+1)); done < <(read_list "$LISTS/scope-cidrs.txt")
  log "scope CIDRs loaded: $n"

  [ "$MODE" = off ] && { log "mode off: no firewall rules"; return; }

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

  # Optional: close the host-proxy bypass. Traffic from the container to the host/gateway goes
  # through the host INPUT chain, not DOCKER-USER (FORWARD), so a proxy bound on the gateway is
  # otherwise reachable. Off by default because it edits the host INPUT chain.
  if [ "$MODE" = enforce ] && [ "$FILTER_GW" = 1 ]; then
    $IPT -N ARTEX-EGRESS-HOST 2>/dev/null || $IPT -F ARTEX-EGRESS-HOST
    $IPT -A ARTEX-EGRESS-HOST -p udp --dport 53 -j RETURN
    $IPT -A ARTEX-EGRESS-HOST -p tcp --dport 53 -j RETURN
    $IPT -A ARTEX-EGRESS-HOST -j REJECT --reject-with icmp-port-unreachable
    $IPT -I INPUT 1 -s "$SUBNET" -d "$GW" -j ARTEX-EGRESS-HOST
    log "gateway filter on: only DNS to $GW allowed from the subnet"
  fi

  if [ "$MODE" = enforce ] && [ -n "$SUBNET6" ] && command -v "$IP6T" >/dev/null 2>&1; then
    $IP6T -I DOCKER-USER 1 -s "$SUBNET6" -m comment --comment "$V6_CMT" -j DROP
    log "IPv6 egress deny installed (subnet6=$SUBNET6)"
  fi

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

# Setup is fail-closed in enforce: install the blanket deny first, and keep it if anything below
# throws. The ERR trap fires under `set -e` before the shell exits.
on_setup_err() { local rc=$?; log "FATAL: setup failed (rc=$rc); fail-closed deny left in place, exiting for restart"; exit "$rc"; }
trap on_setup_err ERR

cleanup_rules
ensure_docker_user
bootstrap_deny
apply_rules
write_dnsmasq_conf
dnsmasq --test -C "$CONF"
remove_bootstrap_deny
trap - ERR   # setup done; a later dnsmasq exit is handled below, not as a fatal setup error.
dnsmasq -k -C "$CONF" &
DNSMASQ_PID=$!
log "dnsmasq listening on $GW:53 (pid $DNSMASQ_PID)"
wait "$DNSMASQ_PID"
# dnsmasq died unexpectedly: leave rules in place (fail closed) and let docker restart us.
log "dnsmasq exited; rules left in place"
exit 1
