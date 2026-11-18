#!/usr/bin/env bash
# ARTEX entry point: one command that dispatches to the existing scripts.
#
#   ./run.sh                        interactive menu
#   ./run.sh install                first-time install (-> install.sh)
#   ./run.sh start [args]           run artex with the supervisor (-> start.sh)
#   ./run.sh update                 upgrade this checkout (-> update.sh)
#   ./run.sh dev                    backend + frontend hot reload (-> dev.sh)
#   ./run.sh build [args]           build/package binaries (-> build.sh)
#   ./run.sh reset-password [args]  reset the admin password (-> reset-password.sh)
set -euo pipefail
cd "$(cd "$(dirname "$0")" && pwd)"

usage(){ sed -n '2,10p' "$0" | sed 's/^# \{0,1\}//'; }

# run_script forwards to one of the existing scripts, passing arguments through.
run_script(){
  local script="$1"; shift
  [ -f "$script" ] || { printf '[x] %s not found\n' "$script" >&2; exit 1; }
  exec bash "$script" "$@"
}

menu(){
  echo "ARTEX — choose an action:"
  echo "  1) install          first-time install"
  echo "  2) start            run artex (supervisor)"
  echo "  3) update           upgrade this checkout"
  echo "  4) dev              backend + frontend hot reload"
  echo "  5) build            build / package binaries"
  echo "  6) reset-password   reset the admin password"
  local choice
  read -rp "> " choice
  case "$choice" in
    1) run_script ./install.sh ;;
    2) run_script ./start.sh ;;
    3) run_script ./update.sh ;;
    4) run_script ./dev.sh ;;
    5) run_script ./build.sh ;;
    6) run_script ./reset-password.sh ;;
    *) printf '[x] invalid choice\n' >&2; exit 1 ;;
  esac
}

cmd="${1:-menu}"
if [ "$#" -gt 0 ]; then shift; fi

case "$cmd" in
  menu)           menu ;;
  install)        run_script ./install.sh "$@" ;;
  start)          run_script ./start.sh "$@" ;;
  update)         run_script ./update.sh "$@" ;;
  dev)            run_script ./dev.sh "$@" ;;
  build)          run_script ./build.sh "$@" ;;
  reset-password) run_script ./reset-password.sh "$@" ;;
  -h|--help|help) usage ;;
  *) printf '[x] unknown command: %s\n\n' "$cmd" >&2; usage; exit 1 ;;
esac
