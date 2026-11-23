#!/usr/bin/env bash
# Capability check. Reports presence; never installs, never fails hard.
# Usage: bash scripts/capcheck.sh [category]

check() { # name, command
  if command -v "$2" >/dev/null 2>&1; then printf "ok      %-22s %s\n" "$1" "$2"
  else printf "MISSING %-22s %s\n" "$1" "$2"; fi
}

checklist() {
  echo "== wordlists"
  for f in /usr/share/wordlists/web/common.txt /usr/share/wordlists/api/api-endpoints.txt \
           /usr/share/wordlists/dns/subs-5000.txt /usr/share/wordlists/passwords/10k-most-common.txt; do
    [ -s "$f" ] && printf "ok      %-22s %s\n" "wordlist" "$f" || printf "MISSING %-22s %s\n" "wordlist" "$f"
  done
  echo "== discovery"
  for t in ffuf gobuster katana arjun dnsx gau; do check discovery "$t"; done
  echo "== web"
  for t in curl jq httpx nuclei nmap whatweb wafw00f nikto commix tplmap hydra; do check web "$t"; done
  echo "== network"
  for t in dig host nslookup smbclient ldapsearch socat chisel responder; do check network "$t"; done
  echo "== ad"
  for t in GetUserSPNs.py secretsdump.py ntlmrelayx.py nxc certipy hashcat john; do check ad "$t"; done
  echo "== deser"
  for t in ysoserial phpggc ysoserial.exe mono java; do check deser "$t"; done
  echo "== cloud"
  for t in aws az gcloud kubectl trivy gitleaks trufflehog; do check cloud "$t"; done
  echo "== privesc"
  for t in linpeas; do check privesc "$t"; done
  [ -s /opt/winPEASany.exe ] && echo "ok      winpeas                /opt/winPEASany.exe" || echo "MISSING winpeas                /opt/winPEASany.exe"
  echo "== mobile"
  for t in jadx apktool frida objection; do check mobile "$t"; done
  echo "== misc"
  for t in sqlmap dalfox python3 perl ruby; do check misc "$t"; done
}

case "${1:-all}" in
  all) checklist ;;
  wordlists) echo "see all" ;;
  *) check "$1" "$1" ;;
esac
