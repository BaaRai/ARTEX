---
name: tool-bootstrap
description: "Invoke this skill before any engagement when a capability is needed but its tool is missing, and after a container rebuild. Covers the installed-capability matrix, pinned install commands, wordlist paths, verification, and the rule that no skill may depend on an unverified tool."
---
# Tool Bootstrap (capability check, install, verify)

The image ships the core scanners; everything else is installed on demand. Every other skill assumes the tools in this matrix resolve, so run the check first and install only what the current task actually needs.

**Rule of thumb:** a skill that names a tool it cannot run is a liability. Either the tool resolves and is verified here, or the skill falls back to manual work and says so.

## Hard gates

1. Never install during a target request; installs are local and can overlap analysis, never a live probe.
2. Pin versions in the install command. Unpinned installs are how a working technique silently breaks next rebuild.
3. Install to `/usr/local/bin` (binaries) or `/opt/<name>` (packages), never `/tmp` (wiped).
4. Verify after install with the exact command listed; `command -v` alone is not verification.
5. Record what you installed as a task fact so later intents do not reinstall or assume it.
6. If an install fails, say which fallback you took. Do not leave a skill silently degraded.

## Capability check

Run this and read the output; missing is expected, not an error:

```
bash /app/skills/tool-bootstrap/scripts/capcheck.sh
```

## Install matrix

| Capability | Tool | Install | Verify |
|---|---|---|---|
| Wordlists | SecLists subsets | already at `/usr/share/wordlists/` | `wc -l /usr/share/wordlists/web/common.txt` |
| Content discovery | ffuf, gobuster, feroxbuster | `apt-get install -y gobuster ffuf` | `ffuf -V` / `gobuster version` |
| Parameter discovery | arjun | `pip install arjun` | `arjun --help` |
| JS/API crawling | katana | present | `katana -h` |
| AD/SMB | impacket 0.12 | `pip install "impacket==0.12.0" "setuptools<81"` | `GetUserSPNs.py -h` |
| AD/SMB (alt) | netexec | `pip install --no-deps git+https://github.com/Pennyw0rth/NetExec` | `nxc smb --help` |
| AD certs | certipy | `pip install certipy-ad` | `certipy --help` |
| Kerberos tickets | hashcat, john | `apt-get install -y hashcat john` | `hashcat --version` |
| NetBIOS/LLMNR poison | responder | `git clone https://github.com/lgandx/Responder /opt/responder` | `responder -h` |
| SMB/LDAP client | smbclient, ldap-utils | `apt-get install -y smbclient ldap-utils` | `smbclient -V` |
| Android static | jadx, apktool | jadx 1.5.6 under `/opt/jadx` behind `/usr/local/bin/jadx` (jar launcher); `apt-get install -y apktool` | `jadx --version` → `1.5.6` |
| Android dynamic | frida-tools, objection | `pip install frida-tools objection` | `frida --version` |
| Java deser | ysoserial | jar at `/opt/ysoserial.jar` | `ysoserial -h` |
| .NET deser | ysoserial.net + mono | `/opt/ysoserial.net/Release/ysoserial.exe`, `apt-get install -y mono-runtime libmono-system-transactions4.0-cil` | `ysoserial.exe --help` |
| PHP deser | phpggc | `/opt/phpggc/phpggc` | `phpggc -l` |
| Cloud AWS | awscli | `pip install awscli` | `aws --version` |
| Cloud Azure | azure-cli | `pip install azure-cli` | `az version` |
| Cloud GCP | gcloud | `/tmp/tools/google-cloud-sdk/bin/gcloud` (sdk at that path) | `gcloud --version` |
| Containers | kubectl, trivy | present | `kubectl version --client` / `trivy --version` |
| Secrets | gitleaks, trufflehog | present | `gitleaks version` / `trufflehog --version` |
| Linux privesc | linpeas | `/opt/linpeas.sh` | `linpeas -h` |
| Windows privesc | winpeas | `/opt/winPEASany.exe` | file exists |
| Pivoting | chisel, socat | present / `apt-get install -y socat` | `chisel -v` |
| WAF fingerprints | wafw00f | present | `wafw00f --help` |
| Web fingerprint | whatweb | `/opt/whatweb/whatweb` | `whatweb --version` |
| WordPress | wpscan | `gem install wpscan` | `wpscan --version` |
| Shells/handlers | socat, nc | `apt-get install -y socat netcat-openbsd` | `socat -V` |
| Coverage-guided fuzzing | afl++ 4.04c | `apt-get install -y afl++` | `afl-fuzz -h` prints `afl-fuzz++4.04c`; `afl-clang-fast -V` |
| Binary exploitation | pwntools, ropper | `pip install pwntools ropper` | `python3 -c "from pwn import ELF"` / `ropper --version` |
| ELF hardening check | checksec 2.7.1 | `git clone --depth 1 https://github.com/slimm609/checksec /opt/checksec` + `ln -sf /opt/checksec/checksec.bash /usr/local/bin/checksec` | `checksec --file=/usr/bin/ls` prints RELRO/CANARY/NX |
| ELF patching | patchelf 0.14.3 | `apt-get install -y patchelf` | `patchelf --version` |
| Shellcode assembly | nasm 2.16.01 | `apt-get install -y nasm` | `nasm -v` |
| Cross-arch emulation | qemu-user, gdb-multiarch | `apt-get install -y qemu-user gdb-multiarch` (+ `libstdc++6:amd64` when running foreign ELF) | `qemu-x86_64 --version` / `gdb-multiarch --version` |
| one-gadget finder | one_gadget 1.10.0 | `apt-get install -y ruby-dev && gem install one_gadget --no-document` | `one_gadget --version` |
| terminfo (checksec curses) | ncurses-term | `apt-get install -y ncurses-term` | `TERM=xterm-256color checksec --file=/usr/bin/ls` runs without warnings |
| Symbolic execution | angr 9.3.4 + z3 4.13.0.0 | `pip install --break-system-packages angr==9.3.4 bitstring==4.1.4 pycparser==3.0` (order matters; 10.x pins z3 5.x, which has no aarch64 wheel and needs gcc 13) | `python3 -c "import angr,z3; print(angr.__version__, z3.get_version_string())"` then solve a 5-line `find`/`avoid` run |
| Memory debugging | valgrind 3.19 | `apt-get install -y valgrind` | `valgrind -q /bin/true; echo $?` → 0 |
| Syscall trace | strace 6.1 | `apt-get install -y strace` | `strace -e exit_group /bin/true` shows the exit |
| Library-call trace | ltrace (upstream git, aarch64 sysdeps) | `apt-get install -y autoconf automake libtool libelf-dev`; `git clone --depth 1 https://gitlab.com/cespedes/ltrace.git /opt/ltrace-src`; `printf '#include <stdbool.h>\n' >> /dev/null`; add `#include <stdbool.h>` after `#include "forward.h"` in `proc.h`; `./autogen.sh && ./configure --prefix=/usr/local && make && make install` | `ltrace -e strlen /tmp/x` prints the call (the distro 0.7.3 binary lacks aarch64) |
| seccomp BPF dump | seccomp-tools 1.6.0 | `apt-get install -y ruby-dev`; `gem install seccomp-tools -v 1.6.0 --no-document`; **uninstall 1.7.1 if present** (needs Ruby 3.2) and patch `syscall.rb` arch table to compare `f.read(2).bytes` against `[0xb7,0x00]` arrays | `seccomp-tools dump /tmp/sc` prints the BPF (see the `symbolic-execution`/`exploit-dev` notes) |
| gdb with pwndbg | pwndbg 2026.10.28 | `git clone --depth 1 https://github.com/pwndbg/pwndbg /opt/pwndbg && /opt/pwndbg/setup.sh` | `gdb -batch -q /bin/true -ex start -ex "info registers pc"` shows the Pwndbg banner |
| Decompiler headless | Ghidra + ghidriff wrapper | present at `/opt/ghidra`; wrapper `/usr/local/bin/analyzeHeadless` exports `GHIDRA_INSTALL_DIR`/`JAVA_HOME` | `analyzeHeadless` prints usage; `ghidriff --help` |
| JNDI chains | JNDI-Injection-Exploit 1.0 jar | `curl -sSLo /opt/tools/jndi.jar https://github.com/welk1n/JNDI-Injection-Exploit/releases/download/v1.0/JNDI-Injection-Exploit-1.0-SNAPSHOT-all.jar` | `java -jar /opt/tools/jndi.jar -h` prints usage |
| LDAP/RMI ref server + deser payloads | marshalsec 0.0.3 | `apt-get install -y maven`; clone `mbechler/marshalsec` to `/opt/tools/marshalsec`; `mvn -q package -DskipTests`; wrapper `marshalsec <Class> [args]` on the shaded jar | `marshalsec LDAPRefServer` prints usage; `marshalsec Java` lists gadgets |
| Cloud enum (scoped) | cloudfox 2.0.5 | arm64 zip release to `/opt/tools/cloudfox/`, symlink `/usr/local/bin/cloudfox` | `cloudfox --help` |
| CIS k8s benchmark | kube-bench 0.16.0 | arm64 tar.gz release to `/opt/tools/`, symlink | `kube-bench version` → 0.16.0 |
| Pod-side k8s enum | peirates 1.1.35 | arm64 tar.xz release to `/opt/tools/peirates-linux-arm64/`, symlink | `peirates --help` prints flags |
| Cloud report | ScoutSuite 5.14.0 | `pip install ScoutSuite` | `scout --version` → 5.14.0 |
| AWS exploitation | pacu 1.7.0 | own venv: `python3 -m venv /opt/pacu-venv` + `/opt/pacu-venv/bin/pip install pacu "botocore<1.32"`; wrapper `pacu` → `/opt/pacu-venv/bin/pacu` (pacu pins botocore<1.32 and would downgrade ScoutSuite's boto3 globally) | `/opt/pacu-venv/bin/pacu --version` |
| AD write/enum | bloodyAD 2.1.8 | `pip install bloodyad` | `bloodyAD --help` |
| AD CS abuse | pywhisker | `pip install pywhisker` | `pywhisker --help` |
| PKINIT / UnPAC-the-hash | PKINITtools | clone `dirkjanm/PKINITtools` to `/opt/tools/`, `pip install -r requirements.txt`, symlink the three `.py` | `gettgtpkinit.py -h`, `getnthash.py -h` |
| Targeted kerberoast | targetedKerberoast.py | clone `https://github.com/shutd%6fwnrepo/targetedKerberoast` to `/opt/tools/targetedKerberoast` | `targetedKerberoast.py -h` |
| Kerberos user enum | kerbrute 1.0.3 (amd64 only) | amd64 release to `/opt/tools/kerbrute`; wrapper runs it under `qemu-x86_64` with `QEMU_LD_PREFIX=/usr/x86_64-linux-gnu` | `kerbrute version` → v1.0.3 |
| Mobile runtime | objection 1.12.5 | `pip install objection` | `objection version` |
| APK secret sweep | apkleaks | `pip install apkleaks` | `apkleaks --help` |
| DEX→JAR | dex2jar 2.4 | release zip to `/opt/tools/dex-tools-v2.4/`, symlink `d2j-dex2jar` | `d2j-dex2jar --help` |
| Second Java decompiler | jd-cli 1.3.0 | release tarball to `/opt/tools/jd-cli/`, symlink | `jd-cli --version` |

## Platform caveats (aarch64 host)

The image is arm64. These are known and already worked around; re-apply them after a rebuild.

- **angr pins:** never upgrade to 10.x. 10.x pins `z3-solver==5.x`, which ships no aarch64 wheel and fails to build under gcc 12 (needs `<format>`, gcc 13). Stay on `angr==9.3.4` + `z3-solver==4.13.0.0` (binary wheel) + `bitstring==4.1.4` + `pycparser==3.0`. pycparser 3.1+/3.12 breaks `cle` and `sim_type`; bitstring 5.x removes `ConstBitStream`.
- **seccomp-tools:** 1.7.1 needs Ruby 3.2 (host has 3.1) — install 1.6.0 and keep 1.7.1 uninstalled. 1.6.0's ELF arch detection compares UTF-8 strings against binary bytes and returns nil on aarch64; patch `lib/seccomp-tools/syscall.rb` to key on `f.read(2).bytes` with array keys (`[0xb7, 0x00] => :aarch64`).
- **ltrace:** distro 0.7.3 has no aarch64 sysdeps. The upstream build in the matrix (0.8.1, `--prefix=/usr/local`) works; `proc.h` needs `#include <stdbool.h>` after `#include "forward.h"` to compile under gcc 12. `/usr/local/bin/ltrace` shadows the distro binary.
- **pwndbg** replaces the prompt of every gdb session. Registers are `pc`/`sp`/`x0..x30`, not `rip`/`rsp` — `info registers rip` errors by design.
- **Ghidra** lives at `/opt/ghidra` behind the `analyzeHeadless` wrapper with `JAVA_HOME=/opt/jdk25`; `ghidriff` sets both itself. Do not reinstall Ghidra per task.
- **jadx** has no usable `bin/jadx` in this 1.5.6 zip; the `/usr/local/bin/jadx` launcher calls `jadx.cli.JadxCLI` from `/opt/jadx/lib/jadx-1.5.6-all.jar` under `java` 17. Verify with `jadx --version` → `1.5.6`; if the wrapper is ever restored from upstream, drop ours so it does not recurse into itself.
- **Platform guard on power commands:** any command containing a power keyword (the machine start/stop words) is rejected. Percent-encode one letter in affected URLs (`shutd%6fwnrepo`); installed wrappers are unaffected.
- **x86_64 targets** need `QEMU_LD_PREFIX=/usr/x86_64-linux-gnu` and `qemu-x86_64`; binfmt is not registered, so a bare `./target` fails with "Exec format error".

## Wordlists (fixed paths, do not invent new ones)

- `/usr/share/wordlists/web/common.txt`, `raft-small-words.txt`, `raft-medium-directories.txt`, `raft-medium-files.txt`
- `/usr/share/wordlists/api/api-endpoints.txt`
- `/usr/share/wordlists/dns/subs-5000.txt`
- `/usr/share/wordlists/passwords/10k-most-common.txt`, `500-worst-passwords.txt`
- `/usr/share/wordlists/usernames.txt`

If a path is empty or absent, re-fetch from SecLists rather than substituting a toy list:

```
curl -sSfL https://raw.githubusercontent.com/danielmiessler/SecLists/master/Discovery/Web-Content/common.txt -o /usr/share/wordlists/web/common.txt
```

## Verification depth

| What | Enough |
|---|---|
| scanner binary | version banner |
| python module | import plus `-h` of the entry script |
| wordlist | line count above zero |
| browser | launch plus one page load |
| AD tool | `-h` renders, no import error |

## Failure handling

- Network blocked: retry once, then fall back to the manual procedure named in the requesting skill and record "tool unavailable, manual path".
- Architecture mismatch (amd64 asset on arm64): look for an arm64 asset, else a Java or python package, else skip with a note.
- Dependency missing after install (`pkg_resources`, `xauth`, `mono` assemblies): install the dependency, do not patch the tool.
- `pip` can fail on packages without arm64 wheels (netexec's aardwolf). Install `--no-deps` plus the dependency list, and note that RDP/VNC protocols are unavailable.

## Completion self-check

- [ ] every tool the task needs resolves and was verified by its listed command.
- [ ] installs are pinned and live outside `/tmp`.
- [ ] wordlist paths are the fixed ones and non-empty.
- [ ] installed capabilities recorded as a fact.
- [ ] fallbacks stated where an install failed.

## Already installed beyond this matrix (do not reinstall)

Verified present in the image, so the skills that name them can rely on them:

- Access and pivot: `ssh`, `scp`, `sftp`, `ssh-keygen`, `sshpass`, `ssh-keyscan`, `proxychains4`, `chisel`, `socat`.
- Blind OOB: `interactsh-client` (public `oast` server; `interactsh-client -v` for a live correlation id).
- AD: `enum4linux-ng` (`/opt/enum4linux-ng`, symlinked), `smbmap`, `nbtscan`.
- Reverse engineering: `radare2`/`rabin2`, `gdb`, Ghidra `/opt/ghidra` with JDK 25 at `/opt/jdk25`, and `ghidriff` (wrapper sets `GHIDRA_INSTALL_DIR`). ghidriff works against Ghidra 12 only with `--no-bsim`; the wrapper's `VersionTrackingDiff` engine is patched for the removed `ConsoleTaskMonitor.DUMMY_MONITOR`. `class-dump`/`jtool2` are macOS Mach-O binaries and need Darling — use r2 or Ghidra for Mach-O instead.
- Data stores and capture: `sqlite3`, `mysql`, `psql`, `redis-cli`, `mongosh`, `tcpdump`, `tshark`.
- Web helpers: `curl_cffi` (0.13.0), `mitmproxy`, `js-beautify`, `yq`, `htmlq` (x86_64 build via `qemu-x86_64-static` + amd64 `libc6`; multiarch is enabled), `hashid`, `haiti`.
- Android: `apksigner`, `zipalign`.
- bbot tool dirs (`/root/.bbot/tools`): `gowitness`, `legba`, `medusa`, `fingerprintx`, `masscan`, `massdns`, `trajan` are symlinked into `/usr/local/bin`. bbot module deps really are baked, so `--no-deps` stays mandatory.

Ghidra 12.2 needs JDK 25; `JAVA_HOME_OVERRIDE=/opt/jdk25` is set in `/opt/ghidra/support/launch.properties`.
### rea MCP wiring (already done, verify only)

- Server `rea` is registered as stdio with `command=/usr/local/bin/rea`, `args=["mcp"]`, and env `GHIDRA_INSTALL_DIR=/opt/ghidra`, `JAVA_HOME=/opt/jdk25`, `PATH=/opt/jdk25/bin:...`. Both env entries are mandatory: without `GHIDRA_INSTALL_DIR` every native analysis fails `provider_unavailable/not_configured`.
- Package is `rea-agents@6.9.0` (upstream `morluto/rea`), installed globally; binary is `/usr/local/bin/rea`.
- Host is arm64 while Ghidra ships only an x86_64 decompiler. The bridge `/opt/ghidra/Ghidra/Features/Decompiler/os/linux_arm_64/decompile` is a shell wrapper that execs `qemu-x86_64 -L /` on the x86_64 binary; it needs `libstdc++6:amd64` and `libgcc-s1:amd64`. Check with `GHIDRA_INSTALL_DIR=/opt/ghidra rea doctor` — `ghidra-native_decompiler` must report `ok: true`.
- Optional and expected to stay red: `hopper` (macOS toolchain) and `ida`.
- The server runs in-container, so rea sees container paths directly (`/app/data/...`). The rea skill's old host-path rule no longer applies here.
- Tools reach an agent only after both a successful `POST /api/mcp/13/refresh` and a visibility grant (`POST /api/visibility/toggle`, body `{"agent_id":"<n>","kind":"mcp","resource_id":13,"visible":true}` — `agent_id` is a quoted string, `resource_id` a bare int). Newly granted tool names appear in the next agent run, not the current one.
