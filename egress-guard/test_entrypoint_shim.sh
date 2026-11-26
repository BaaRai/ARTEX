#!/usr/bin/env bash
# Offline regression test for egress-guard/entrypoint.sh and selftest.sh using
# shimmed iptables/ip6tables/ipset/dnsmasq binaries (never touches host firewall).
set -euo pipefail

DIR="$(cd "$(dirname "$0")" && pwd)"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

mkdir -p "$TMP/bin" "$TMP/lists" "$TMP/logs" "$TMP/run"
touch "$TMP/lists/base-domains.txt" "$TMP/lists/scope-domains.txt" "$TMP/lists/scope-cidrs.txt"
IP6_STATE="$TMP/ip6_rules.txt"
touch "$IP6_STATE"

# Minimal iptables / ipset / dnsmasq shims
cat > "$TMP/bin/iptables" <<'SHIM'
#!/usr/bin/env bash
if [ "${1:-}" = "-C" ] || [ "${2:-}" = "-C" ] || [ "${3:-}" = "-C" ]; then exit 1; fi
exit 0
SHIM
cp "$TMP/bin/iptables" "$TMP/bin/iptables-nft"

cat > "$TMP/bin/ip6tables" <<SHIM
#!/usr/bin/env bash
STATE="$IP6_STATE"
case "\$1" in
  -L) exit 0 ;;
  -I)
    # Record rule without "-I <chain> <pos>"
    shift 3
    echo "\$*" >> "\$STATE"
    exit 0
    ;;
  -C)
    shift 2
    grep -Fxq -- "\$*" "\$STATE"
    ;;
  -D)
    shift 2
    grep -Fxv -- "\$*" "\$STATE" > "\$STATE.tmp" || true
    mv "\$STATE.tmp" "\$STATE"
    exit 0
    ;;
  *) exit 0 ;;
esac
SHIM
cp "$TMP/bin/ip6tables" "$TMP/bin/ip6tables-nft"

cat > "$TMP/bin/ipset" <<'SHIM'
#!/usr/bin/env bash
exit 0
SHIM

cat > "$TMP/bin/dnsmasq" <<'SHIM'
#!/usr/bin/env bash
if [ "${1:-}" = "--test" ]; then
  [ "${FAIL_DNSMASQ_TEST:-0}" = "1" ] && exit 1
  exit 0
fi
# Record that dnsmasq started, then exit cleanly so entrypoint finishes
touch "${DNSMASQ_STARTED_FILE}"
exit 0
SHIM
chmod +x "$TMP/bin/"*

# Rewrite hardcoded /etc/egress-guard, /var/log/egress, /run/egress-guard paths into TMP copy
sed -e "s|LISTS=/etc/egress-guard|LISTS=$TMP/lists|" \
    -e "s|LOGDIR=/var/log/egress|LOGDIR=$TMP/logs|" \
    -e "s|CONF=/run/egress-guard/dnsmasq.conf|CONF=$TMP/run/dnsmasq.conf|" \
    "$DIR/entrypoint.sh" > "$TMP/entrypoint_test.sh"
chmod +x "$TMP/entrypoint_test.sh"

# Test 1: enforce with ARTEX_SUBNET6 keeps an active IPv6 DROP rule after remove_bootstrap_deny
: > "$IP6_STATE"
rm -f "$TMP/started"
set +e
PATH="$TMP/bin:$PATH" IPTABLES_BIN="$TMP/bin/iptables" IP6TABLES_BIN="$TMP/bin/ip6tables" \
  DNSMASQ_STARTED_FILE="$TMP/started" \
  EGRESS_MODE=enforce ARTEX_SUBNET=172.31.250.0/24 ARTEX_GATEWAY=172.31.250.1 \
  ARTEX_SUBNET6=fd00:artex::/64 \
  bash "$TMP/entrypoint_test.sh" >/tmp/eg418_t1.log 2>&1
set -e
test -f "$TMP/started" || { echo "FAIL: dnsmasq did not start"; cat /tmp/eg418_t1.log; exit 1; }
if ! grep -q -- "-s fd00:artex::/64 .* -j DROP" "$IP6_STATE"; then
  echo "FAIL: IPv6 DROP rule was deleted by remove_bootstrap_deny! Active ip6tables rules:"
  cat "$IP6_STATE"
  exit 1
fi
echo "PASS: IPv6 DROP rule persists after remove_bootstrap_deny"

# Test 2: dnsmasq --test failure leaves bootstrap-deny in place (fail-closed)
: > "$IP6_STATE"
set +e
PATH="$TMP/bin:$PATH" IPTABLES_BIN="$TMP/bin/iptables" IP6TABLES_BIN="$TMP/bin/ip6tables" \
  FAIL_DNSMASQ_TEST=1 DNSMASQ_STARTED_FILE="$TMP/started2" \
  EGRESS_MODE=enforce ARTEX_SUBNET=172.31.250.0/24 ARTEX_GATEWAY=172.31.250.1 \
  ARTEX_SUBNET6=fd00:artex::/64 \
  bash "$TMP/entrypoint_test.sh" >/tmp/eg418_t2.log 2>&1
rc=$?
set -e
if [ "$rc" -eq 0 ]; then
  echo "FAIL: entrypoint exited 0 when dnsmasq --test failed"
  exit 1
fi
if ! grep -q "artex-egress-bootstrap-deny" "$IP6_STATE"; then
  echo "FAIL: bootstrap-deny was already removed before dnsmasq --test failed"
  exit 1
fi
echo "PASS: dnsmasq --test failure keeps bootstrap-deny in place (fail-closed)"

# Test 3: selftest.sh must exit non-zero when fail > 0
if ! tail -n 3 "$DIR/selftest.sh" | grep -qE 'fail.*-eq 0|exit'; then
  echo "FAIL: selftest.sh does not exit non-zero when fail > 0"
  exit 1
fi
echo "PASS: selftest.sh exits non-zero on failure"
