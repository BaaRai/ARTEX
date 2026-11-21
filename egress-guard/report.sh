#!/usr/bin/env bash
# Calibration report for egress-guard.
#  1) destinations the firewall flagged (would be / were blocked), mapped back to DNS names
#  2) names the ARTEX container resolved that are NOT covered by the allowlist
# Usage: egress-guard/report.sh [since]   (default since: "24 hours ago"; journalctl syntax)
# Reading the kernel log needs the adm/systemd-journal group or sudo.
set -euo pipefail
cd "$(dirname "$0")"
SINCE="${1:-24 hours ago}"
LOG=logs/dnsmasq.log

suffixes() { sed -e 's/#.*//' -e 's/[[:space:]]//g' lists/base-domains.txt lists/scope-domains.txt | tr 'A-Z' 'a-z' | sed -e 's/^\*\.//' -e 's/^\.//' | grep -v '^$' || true; }

declare -A ip2name
if [ -f "$LOG" ]; then
  while read -r name ip; do ip2name[$ip]="$name"; done < <(awk '/ reply / && / is [0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$/ {for(i=1;i<=NF;i++) if($i=="reply"){print $(i+1), $NF}}' "$LOG")
fi

echo "=== 1) Conexões sinalizadas pelo firewall (desde: $SINCE) ==="
if journalctl -k --since "$SINCE" -q --no-pager -g 'ARTEX-EGRESS-' >/dev/null 2>&1; then
  journalctl -k --since "$SINCE" -q --no-pager -g 'ARTEX-EGRESS-' \
    | grep -oE 'ARTEX-EGRESS-[a-z]+ .*DST=[0-9.]+.*PROTO=[A-Z]+( .*DPT=[0-9]+)?' \
    | sed -E 's/^ARTEX-EGRESS-([a-z]+) .*DST=([0-9.]+).*PROTO=([A-Z]+)( .*DPT=([0-9]+))?.*/\1 \2 \3 \5/' \
    | sort | uniq -c | sort -rn \
    | while read -r cnt mode ip proto port; do printf '%6s  %-8s %-16s %-5s %-6s %s\n' "$cnt" "$mode" "$ip" "$proto" "${port:--}" "${ip2name[$ip]:-(sem DNS: IP direto)}"; done
else
  echo "(sem acesso ao log do kernel: rode com sudo ou adicione o usuário ao grupo adm)"
fi

echo
echo "=== 2) Nomes resolvidos fora da allowlist (candidatos a liberar ou suspeitos) ==="
if [ -f "$LOG" ]; then
  mapfile -t sfx < <(suffixes)
  awk '/ query\[(A|AAAA)\] / {for(i=1;i<=NF;i++) if($i ~ /^query\[/){print tolower($(i+1))}}' "$LOG" | sort | uniq -c | sort -rn \
    | while read -r cnt name; do
        ok=0; for s in "${sfx[@]}"; do [[ "$name" == "$s" || "$name" == *".$s" ]] && { ok=1; break; }; done
        [ $ok -eq 0 ] && printf '%6s  %s\n' "$cnt" "$name"
      done
else
  echo "(sem $LOG ainda)"
fi
