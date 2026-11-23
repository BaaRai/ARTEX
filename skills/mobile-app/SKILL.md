---
name: mobile-app
description: "Invoke this skill when the package is in scope and an APK, IPA or HAP must be assessed — static decoding, manifest and entitlement review, exported components and deep links, hardcoded secrets, storage, pinning, and Frida-backed dynamic checks. Static-first, emulator optional."
---
# Mobile Application Testing

Under an **authorized** engagement only, and only for a package the program names. Downloading from a store is fine; an unevenly published build is not the tested artifact.

Static first: most findings do not need a device. Dynamic work is added only when the app resists static review or the task explicitly asks for runtime proof.

## Hard gates

1. Pin the artifact: package id, version, hash. Anything found applies to that hash only.
2. No store account credentials unless provided; use a local emulator when dynamic work is needed.
3. Traffic from the app is in scope only for the hosts named in `scope.md`, even if the app talks to more.
4. Never install a production build over an existing app without noting data loss.

## Phase 1: decode

```
apktool d -f -o decoded app.apk            # resources + manifest
jadx -d jadx-out app.apk                   # Java sources (1.5.6, jar launcher)
d2j-dex2jar classes.dex -o app.jar         # when jadx trips on obfuscation
jd-cli app.jar -od jd-out                  # second decompiler opinion (different failure modes)
apkleaks -f app.apk -o apkleaks.json       # automated secret sweep, then verify each hit by hand
```

`objection` is installed (1.12.5) for the runtime phase: `objection -g <pkg> explore` then `android sslpinning disable`, `android root disable`, `env`, `memory search`.

- IPA: unzip, then `class-dump`/`jtool2` or just inspect `Payload/*.app/` (Info.plist, entitlements, binaries).
- HAP: unzip, `pack.info`, module.json.
- Keep a manifest summary at the top of the artifact: package, versionCode, `minSdk`, `debuggable`, `allowBackup`, `cleartextTrafficPermitted`, permissions.

## Phase 2: manifest and components

- Exported components: `exported="true"` activities, services, receivers, providers without permission guards.
- Deep links: `<intent-filter>` with `VIEW`/`BROWSABLE` + scheme/host — test each with `adb shell am start -W -a android.intent.action.VIEW -d "<uri>"`.
- `android:allowBackup="true"`, `debuggable="true"`, `usesCleartextTraffic="true"`.
- Content providers with `grantUriPermissions` and no path permission — path traversal in `openFile`.
- Task hijacking: `taskAffinity` + `launchMode` on the launcher activity.

## Phase 3: secrets and config

```
grep -rInE "api[_-]?key|secret|token|password|BEGIN (RSA|EC) PRIVATE KEY" decoded jadx-out
```

- `strings` on `lib/**/*.so` for endpoints and keys.
- Firebase config (`google-services.json`), maps keys (restricted?), Stripe publishable vs secret.
- Hardcoded endpoints to staging/dev hosts — check whether they are in scope and alive.
- Certificate material in `res/raw` or assets.

## Phase 4: storage and transport

- Static: SharedPreferences writes, `getExternalFilesDir`, SQLite schemas, log statements around tokens.
- Rules: secret in `SharedPreferences` on a non-rooted backup-enabled app is a real leak (`adb backup`); token in logs is a leak.
- Network config: look for `network_security_config.xml` trust anchors, pinning implementation (`CertificatePinner`, `TrustManager`, `NSURLSessionDelegate`), and whether pinning is enforced in release.

## Phase 5: dynamic (Frida)

- Emulator plus the release APK; `frida-ps -U` to confirm the server.
- Pinning bypass: `objection -g <pkg> explore` then `android sslpinning disable`; without objection, a `frida` script hooking `TrustManager`/`CertificatePinner`.
- Root/debug detection bypass the same way; note which checks existed.
- Then drive the app through the in-scope flows with the platform proxy (`127.0.0.1:8788`) as the device proxy, and capture in the traffic view.
- Storage observation: dump the app's data dir on a rooted emulator, confirm what the app persists.

## Phase 6: report shape

- Artifact hash, component, exposure (exported/permission), and the exact `adb`/`frida` reproduction.
- Severity anchors: exported provider with file read = high; pinning absent = low/medium (report only with a working MITM); secret in backup-enabled prefs = medium; debug build in production = medium.

## False-positive traps

- `exported="true"` without a permission but also without a useful intent path.
- A key that is public by design (Firebase API key, Maps key).
- Pinning "present" in code but only in a debug build variant.
- Deep link that opens the app but lands on the login screen with no parameter use.
- Emulator-only failures read as app bugs.

## Tool and pacing table

| Tool | Use | Flags |
|---|---|---|
| apktool / jadx | decode | local |
| grep / strings | secret discovery | local |
| adb | component and deep-link triggers | serial |
| frida / objection | pinning, runtime hooks | serial |
| proxy | traffic capture | platform pace profile |

## Completion self-check

- [ ] artifact hash and version recorded.
- [ ] manifest summary written before grepping.
- [ ] each exported component tested, not just listed.
- [ ] secrets validated against the provider when possible.
- [ ] dynamic checks marked which build variant they applied to.