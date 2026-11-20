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
| `monitor` (default) | only logs what would be blocked |
| `enforce` | blocks |
| `off` | no rules; DNS only |

## Operation
- New engagement: edit `lists/scope-domains.txt` and `lists/scope-cidrs.txt`, then
  `docker compose -f docker-compose.yml -f docker-compose.egress.yml restart egress-guard`.
- Calibrate: `sudo egress-guard/report.sh` (or pass `"48 hours ago"`). Lists flagged
  connections and resolved names outside the allowlist.
- Turn blocking on: set `EGRESS_MODE=enforce` in `.env` and bring the service up again.
- Stopping the service removes the rules. If dnsmasq dies on its own, the rules stay
  (fail-closed).

## Known gaps
- Exfiltration through an allowed destination (a GitHub gist, OAST servers, an
  in-scope host the attacker controls).
- Data hidden in DNS queries (slow; visible in `logs/dnsmasq.log`).
- CDN-shared IPs: allowing one name allows the IP for other sites on the same IP.
