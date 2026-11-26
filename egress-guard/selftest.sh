#!/usr/bin/env bash
# egress-guard self-test. Builds nothing; run it inside the image, in the container's
# OWN network namespace (NOT network_mode host), so it exercises the real ipset + iptables
# without touching the host firewall:
#
#   docker build -t artex-egress-guard:local ./egress-guard
#   docker run --rm --cap-add NET_ADMIN --cap-add NET_RAW \
#     -v "$PWD/egress-guard/selftest.sh:/selftest.sh:ro" \
#     --entrypoint bash artex-egress-guard:local /selftest.sh
#
# Checks: startup flush of a stale allow IP (#1), rules applied, enforce REJECT (#5),
# DNS redirect, and fail-closed on a setup error (#2: deny left in place, FATAL, exit 1).
set -u
IPT=iptables; command -v iptables-nft >/dev/null 2>&1 && IPT=iptables-nft
pass=0; fail=0
ok(){ echo "PASS: $1"; pass=$((pass+1)); }
no(){ echo "FAIL: $1"; fail=$((fail+1)); }

# dummy iface so dnsmasq can bind the gateway IP
ip link add dummy0 type dummy 2>/dev/null
ip addr add 10.123.0.1/24 dev dummy0 2>/dev/null
ip link set dummy0 up 2>/dev/null

echo "######## TEST 1: happy path (enforce) ########"
# seed a STALE allowed IP to prove flush at startup
ipset create artex_allow hash:ip timeout 21600 -exist
ipset add artex_allow 203.0.113.99 -exist
export EGRESS_MODE=enforce ARTEX_SUBNET=10.123.0.0/24 ARTEX_GATEWAY=10.123.0.1 UPSTREAM_DNS=1.1.1.1
/usr/local/bin/egress-guard >/tmp/g.log 2>&1 &
GP=$!
sleep 3
if ipset list artex_allow 2>/dev/null | grep -q 203.0.113.99; then no "stale IP flushed at startup"; else ok "stale IP flushed at startup (#1)"; fi
$IPT -S DOCKER-USER 2>/dev/null | grep -q 'ARTEX-EGRESS' && ok "DOCKER-USER jumps to ARTEX-EGRESS" || no "DOCKER-USER jump present"
$IPT -S DOCKER-USER 2>/dev/null | grep -q 'bootstrap-deny' && no "bootstrap-deny removed after clean setup" || ok "bootstrap-deny removed after clean setup (#2)"
$IPT -S ARTEX-EGRESS 2>/dev/null | grep -q 'match-set artex_allow dst' && ok "allow ipset rule present" || no "allow ipset rule present"
$IPT -S ARTEX-EGRESS 2>/dev/null | grep -qi 'REJECT' && ok "enforce REJECT present (#5)" || no "enforce REJECT present"
$IPT -t nat -S ARTEX-DNS 2>/dev/null | grep -q 'DNAT' && ok "DNS redirect (DNAT) present" || no "DNS redirect present"
kill -TERM $GP 2>/dev/null; sleep 1
$IPT -S ARTEX-EGRESS 2>/dev/null | grep -q . && no "chain removed on stop" || ok "chain removed on stop (cleanup)"

echo "######## TEST 2: fail-closed (setup error after bootstrap) ########"
# invalid IPSET_TIMEOUT makes apply_rules fail right after bootstrap_deny is installed
export IPSET_TIMEOUT=notanumber
/usr/local/bin/egress-guard >/tmp/g2.log 2>&1
rc=$?
[ "$rc" -ne 0 ] && ok "setup failure is fatal (exit $rc, #2 fail-closed)" || no "setup failure fatal"
$IPT -S DOCKER-USER 2>/dev/null | grep -q 'bootstrap-deny.*DROP' && ok "fail-closed DROP left in place (#2)" || no "fail-closed DROP present"
grep -q 'FATAL: setup failed' /tmp/g2.log && ok "operator sees FATAL message (#2 documented)" || no "FATAL message logged"
# cleanup
unset IPSET_TIMEOUT
$IPT -F DOCKER-USER 2>/dev/null; $IPT -X ARTEX-EGRESS 2>/dev/null
echo "=== guard log test1 ==="; cat /tmp/g.log; echo "=== guard log test2 ==="; cat /tmp/g2.log
echo "######## RESULT: pass=$pass fail=$fail ########"
[ "$fail" -eq 0 ]
