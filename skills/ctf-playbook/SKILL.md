---
name: ctf-playbook
description: "CTF and lab challenges: crypto, pwn/heap/kernel exploitation, reverse engineering, forensics and steganography, misc jails and encodings, malware analysis, OSINT, web challenges, hardware/RF, blockchain. Index of 73 references. Lab use only."
---

# CTF playbook (index)

The largest pack here: seventy-three references under `references/`, organised
by challenge category. This is competition and lab material.

CTF work is bounded differently from client work. The target is a challenge
designed to be attacked, and the goal is the flag. That difference changes the
risk calculus, not the discipline.

## Scope

1. **Challenge infrastructure only.** The CTF platform, the provided instance,
   and the files handed to you. Attacking the scoring server, other teams, or the
   platform's shared services is cheating and, on some platforms, a crime.
2. **Shared instances are shared.** Other competitors are on the same host. Do
   not run denial-of-service payloads against a shared challenge instance.
3. **Lab and competition only.** Techniques here (kernel exploitation, malware
   analysis, jamming) do not transfer to client engagements without explicit
   authorization. Anything in this pack that touches radio, hardware, or live
   systems needs the same gates as `post-exploit`.
4. **No real-world targeting.** OSINT challenges use synthetic data. Applying
   those techniques to real people is out of bounds and, for personal data,
   illegal in most jurisdictions.

## Environment

Challenge categories and what this image supports:

| Category | Support |
|---|---|
| Crypto | Full — `python3` with stdlib, write your own scripts |
| Web | Full — see `web-vuln`, `payload-cookbook` |
| Forensics | Mostly — `python3`, `strings`, `xxd`, `file`; **no** `binwalk`, `volatility`, `wireshark`/`tshark` |
| Stego | Partial — `python3` (PIL not guaranteed); no `steghide`, `zsteg` |
| Reverse | Partial — `objdump`, `readelf`, `strings`, `file` present; **no** `ghidra`, `radare2`/`rizin`, `jadx`, `apktool`, `IDA`, `x64dbg` |
| Pwn | Partial — `python3` only; **no** `pwntools`, `gdb`, `ROPgadget`, `checksec` |
| Malware | Partial — static reading with `strings`/`ripgrep`; **no** sandbox, no detonation tooling |
| Misc / jails | Good — `python3`, `bash`, `nc` |
| OSINT | Good — `curl`, `dig`, `whois`, `jq` |
| Hardware / RF | Method only — no SDR, no logic analyzer |
| Blockchain | Method only — no `foundry`, `hardhat`, `cast` |

Where a binary challenge needs debugging, the references describe the intended
approach; you can implement equivalent logic in `python3` against the
disassembly. Say what you could not do rather than implying a working exploit.

## Choosing a reference

**Crypto** — start at `references/crypto.md` for the decision tree, then:
`crypto-rsa-attacks.md`, `crypto-ecc-attacks.md`, `crypto-classic-ciphers.md`,
`crypto-modern-ciphers.md`, `crypto-prng.md`, `crypto-advanced-math.md`,
`crypto-zkp-and-advanced.md`, `crypto-historical.md`, `crypto-exotic-crypto.md`

**Pwn** — `references/pwn.md` for orientation, then:
`pwn-overflow-basics.md`, `pwn-format-string.md`, `pwn-rop-and-shellcode.md`,
`pwn-rop-advanced.md`, `pwn-advanced.md`, `pwn-advanced-exploits.md`,
`pwn-advanced-exploits-2.md`, `heap-advanced.md`, `pwn-kernel.md`,
`pwn-kernel-techniques.md`, `pwn-kernel-bypass.md`, `pwn-sandbox-escape.md`,
`kernel-exploitation.md`

**Reverse** — `references/reversing.md`, then:
`reverse-tools.md`, `reverse-tools-dynamic.md`, `reverse-tools-advanced.md`,
`reverse-patterns.md`, `reverse-patterns-ctf.md`, `reverse-patterns-ctf-2.md`,
`reverse-platforms.md`, `reverse-languages.md`, `reverse-languages-compiled.md`,
`reverse-anti-analysis.md`, `wasm-challenge.md`, `android-ctf.md`

**Forensics** — `references/forensics.md`, then:
`forensics-disk-and-memory.md`, `forensics-disk-recovery.md`,
`forensics-windows.md`, `forensics-linux-forensics.md`,
`forensics-network.md`, `forensics-network-advanced.md`,
`forensics-steganography.md`, `forensics-stego-advanced.md`,
`forensics-signals-and-hardware.md`, `forensics-3d-printing.md`

**Misc** — `misc-encodings.md`, `misc-pyjails.md`, `misc-bashjails.md`,
`misc-dns.md`, `misc-games-and-vms.md`, `misc-games-and-vms-2.md`,
`misc-rf-sdr.md`

**Malware** — `malware-scripts-and-obfuscation.md`, `malware-pe-and-dotnet.md`,
`malware-c2-and-protocols.md`

**OSINT** — `osint-web-and-dns.md`, `osint-social-media.md`,
`osint-geolocation-and-media.md`

**Web challenges** — `web-server-side.md`, `web-server-side-advanced.md`,
`web-server-side-exec.md`, `web-server-side-deser.md`, `web-client-side.md`,
`web-auth-and-access.md`, `web-auth-jwt.md`, `web-auth-infra.md`,
`web-node-and-prototype.md`, `web-cves.md`, `web-web3.md`

**Blockchain** — `web3.md` (challenge-oriented; the audit-oriented pack is
`web3-playbook`)

**Method overview** — `../recon-playbook/references/ctf-methodology.md` (the
general recon angle lives in `recon-playbook`).

## Method

1. **Read the challenge text twice.** Category, constraints, and what is handed
   to you usually narrow the technique to two or three options.
2. **Triage the artifact** — `file`, `strings`, headers, entropy — before any
   tooling.
3. **Write a script rather than doing arithmetic by hand.** CTF repetition is
   where scripts save the most time, and `python3` is always available.
4. **Record the technique, not just the flag.** The reusable insight is the
   exploit primitive, not the answer string.

## Boundaries

- Do not attack the CTF platform itself.
- Do not apply live-target techniques to systems outside the challenge.
- Radio, hardware, and malware material stays in the lab.
