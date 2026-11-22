---
name: firmware-iot
description: "Invoke this skill when a firmware image or IoT device is in scope — extraction with unblob or binwalk, filesystem inspection, init and update paths, hardcoded keys, web UI and service review, and QEMU-based emulation when the device is absent."
---
# Firmware and IoT

Under an **authorized** engagement only, and only for images/devices the program names. Firmware is a filesystem review plus an update-chain review; the device UI is a web app and belongs to `web-vuln-testing` once it is reachable.

## Hard gates

1. Work on a copy; never modify the original image.
2. Prefer unblob (correct extraction); binwalk is the fallback for formats unblob rejects.
3. Emulation is best-effort: many images need the real hardware. State when an emulation result is inconclusive.
4. No flashing a device unless the task says so.
5. Device traffic counts against the quiet profile; one request in flight.

## Phase 1: identify and extract

```
file image.bin
unblob -e extract image.bin
```

- Fallbacks: `binwalk -eM image.bin`; `unsquashfs -d rootfs <squashfs>`; `dd` at the offsets binwalk reports.
- Recursive extraction: unblob handles nesting; run it again inside outputs.
- If nothing extracts, check for encrypted/compressed containers (UBI, JFFS2, vendor headers) and note the blocker rather than guessing.

## Phase 2: filesystem review

- Layout: `etc/init.d/`, `etc/rc.local`, `etc/inittab`, systemd units, `usr/sbin`, `www/`, `/var/www`.
- Credentials: `/etc/passwd`, `/etc/shadow`, `etc/config/*`, `/etc/*.conf` with `password`, `key`, `psk`; grep `BEGIN .*PRIVATE KEY`.
- Per-device secrets: certificate files, MQTT/TR-069 credentials, cloud API keys.
- Update mechanism: scripts under `usr/sbin` or `etc` calling `curl`/`wget`; URL scheme (http), signature check present?, rollback.
- Debug paths: telnetd started in init, dropbear/ssh keys with empty passphrase, `debug` flags.
- SUID binaries and world-writable dirs: `find rootfs -perm -4000`; note the busybox applet set.

## Phase 3: services and web UI

- Identify listeners from init/config (`httpd`, `lighttpd`, `upnpd`, `mqtt`), their ports and auth.
- CGI scripts: `www/cgi-bin/` — read for command injection (`system(`, `popen`) and parameter parsing.
- Static web review is valid: hardcoded JS secrets, API tokens, debug endpoints.
- If the device or an emulation is reachable, run `web-vuln-testing` against it, one host at a time.

## Phase 4: emulation (optional)

- Extract the rootfs, then run the web binary under `qemu-<arch>-static` with a chroot:
```
sudo chroot rootfs /usr/bin/qemu-arm-static /usr/sbin/httpd
```
- Bind to a local port and reach it through `127.0.0.1:<port>`.
- Common failures: NVRAM (`nvram_get`), missing `/dev`, vendor kernel modules. Stub what is missing and record it.
- `firmadyne`/`firmAE` are useful but heavy; use only when the task requires emulation.

## Phase 5: impact framing

| Finding | Notes |
|---|---|
| update served over http, no signature | high — network attacker to root |
| hardcoded unique device key/private key | high |
| telnetd/ssh with default creds | high |
| CGI command injection | critical |
| verbose debug interface | medium |

State whether a finding is per-device or fleet-wide; a shared default key is fleet-wide.

## False-positive traps

- Password hash strings that are placeholders (`*`, `x`, `!`).
- A signature check that exists in the updater but is not enforced (read the branch).
- Telnet binary present but never started.
- Emulation working for the UI but not for the NVRAM-dependent flows.
- Key material that is only a public certificate.

## Tool and pacing table

| Tool | Use | Flags |
|---|---|---|
| unblob | extraction | local |
| binwalk | fallback extraction | `-eM` |
| unsquashfs | squashfs | local |
| qemu-*-static | emulation | local |
| curl | reachable UI | one request in flight |

## Completion self-check

- [ ] image hash recorded.
- [ ] extraction method stated, failures named.
- [ ] update path fully read, including signature enforcement.
- [ ] per-device vs fleet-wide marked.
- [ ] emulation limits written down where used.