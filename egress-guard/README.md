# egress-guard

DNS-driven egress allowlist for the ARTEX container. It blocks data from leaving
to arbitrary hosts (e.g. a malicious PoC sending the keys to an attacker) without
breaking asset discovery: any subdomain of an in-scope domain is allowed
automatically the moment it resolves.

Opt-in and Linux-only. It is a separate compose file and needs `NET_ADMIN`; it
edits the host firewall, so read this before enabling it.

## How it works
1. ARTEX uses `172.31.250.1` (this service's dnsmasq) as its DNS. DNS queries to
   any other server (dnsx, bbot, `dig @8.8.8.8`) are redirected here.
2. Names matching a suffix in `lists/base-domains.txt` or `lists/scope-domains.txt`
   have their resolved IPs added to the `artex_allow` ipset (they expire 6 h after
   the last query).
3. In the `DOCKER-USER` chain, new outbound connections from the ARTEX network are
   allowed only if the destination is in `artex_allow` or in `lists/scope-cidrs.txt`.
   Everything else is logged (`ARTEX-EGRESS-*` in the kernel log) and, in
   `enforce`, rejected.

## Modes (`EGRESS_MODE` in `.env`)
| Mode | Effect |
|---|---|
| `monitor` (default) | **observe only** — logs what would be blocked, blocks nothing |
| `enforce` | the allowlist actually blocks |
| `off` | no firewall rules; DNS redirect only |

**The allowlist only restricts egress in `enforce`.** The default `monitor` is a
dry run for calibration — it protects nothing. Set `EGRESS_MODE=enforce` (and
bring the service up again) for the guard to have any effect. In `enforce`,
setup is **fail-closed**: a blanket deny is installed before the allowlist is
built, so if setup fails the ARTEX container loses outbound connectivity (except
DNS) until the guard restarts cleanly.

## Operation
- New engagement: edit `lists/scope-domains.txt` and `lists/scope-cidrs.txt`, then
  `docker compose -f docker-compose.yml -f docker-compose.egress.yml restart egress-guard`.
- Calibrate: `sudo egress-guard/report.sh` (or pass `"48 hours ago"`). Lists flagged
  connections and resolved names outside the allowlist.
- Turn blocking on: set `EGRESS_MODE=enforce` in `.env` and bring the service up again.
- Stopping the service removes the rules. If dnsmasq dies on its own, the rules stay
  (fail-closed).

## Known gaps
- **IPv6 is not filtered.** The allowlist is IPv4-only (ipset `hash:ip`/`hash:net`).
  Keep the ARTEX network IPv4-only (do not set `enable_ipv6` on it) — then the
  container has no IPv6 address and the gap is latent. If you do enable IPv6, set
  `ARTEX_SUBNET6` so `enforce` installs an `ip6tables` deny for it; the guard logs
  a warning when IPv6 is left unfiltered.
- **Traffic to the host/gateway bypasses the filter.** Container→gateway traffic
  goes through the host `INPUT` chain, not `DOCKER-USER` (`FORWARD`), so a proxy
  bound on the gateway IP is reachable. Don't run a forward proxy on the gateway,
  or set `EGRESS_FILTER_GATEWAY=1` to have `enforce` add an `INPUT` rule that drops
  everything from the subnet to the gateway except DNS (off by default — it edits
  the host `INPUT` chain).
- Exfiltration through an allowed destination (a GitHub gist, OAST servers, an
  in-scope host the attacker controls).
- Data hidden in DNS queries (slow; visible in `logs/dnsmasq.log`).
- CDN-shared IPs: allowing one name allows the IP for other sites on the same IP.
