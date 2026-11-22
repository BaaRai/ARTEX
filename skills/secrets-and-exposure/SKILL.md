---
name: secrets-and-exposure
description: "Invoke this skill when the hunt needs exposed material found and triaged — .git and SVN dumps, .env and config backups, source maps, keys in JS bundles, directory listings, logs, Actuator and phpinfo, storage buckets, and leaked credentials. Includes the live-versus-dead key triage rule."
---
# Secrets and Exposure

Exposed material is the cheapest path from recon to access. Under an **authorized** engagement only. The core discipline: an exposure is only a finding when the material is confirmed valid *and* its scope is known; otherwise it is an observation.

## Hard gates

1. Recover, do not destroy: `.git` dump, then `git status` locally; never `git push` back.
2. Validate a credential with **one** read-only call. No writes with a discovered key unless the program allows it.
3. A key for a third-party service counts only if that service is in scope or explicitly named; note the ownership boundary.
4. Bucket and storage probes: HEAD/GET on the listing only; no upload.
5. Evidence includes the recovery URL and the validation response, not just the secret string.

## Phase 1: version control and backups

- `/.git/HEAD`, `/.git/config`, `/.git/index`, `/.svn/entries`, `/.hg/`.
- Confirm a real repo before dumping: `HEAD` returns `ref: refs/heads/...` and `config` contains `[core]`.
- Dump offline:
```
mkdir repo && cd repo
for p in HEAD config index; do curl -sS "https://<host>/.git/$p" -o "$p"; done
# recover the rest from the index object list, or use git-dumper
```
- `git-dumper https://<host>/.git ./repo` if present (pip install git-dumper).
- Then locally: `git log --oneline | head`, `git show`, grep for `password`, `secret`, `.env`, internal hosts. Deleted-but-committed secrets are the usual prize.
- Backup names: `backup.zip`, `www.zip`, `db.sql`, `site.tar.gz`, `index.php.bak`, `config.php.old`, `~user`, `.DS_Store`.

## Phase 2: config and environment

- `/.env`, `/.env.local`, `/.env.prod`, `/config.json`, `/appsettings.json`, `/wp-config.php.bak`.
- Invalid key/value shape means a rewrite page, not the file — check for `key=` lines.
- Record every credential and internal hostname; classify each as browser-side, server-side or infra.

## Phase 3: JS bundles and source maps

- `//# sourceMappingURL=` trailers; fetch the `.map` and reconstruct sources.
- `api-recon` already harvests endpoints and keys; this phase triages what it found.
- Look for: cloud keys, bearer tokens, internal hostnames, feature flags, admin bundles, staging URLs.
- Distinguish build-time public keys (Stripe pk_, Firebase config, Sentry DSN) from secrets.

## Phase 4: server surfaces

- Directory listing: `/uploads/`, `/backup/`, `/files/`, `/static/` (`Index of /`).
- Logs: `/debug.log`, `/storage/logs/laravel.log`, `/logs/`, `/error_log`.
- Debug and info endpoints: `/phpinfo.php`, `/server-status`, `/actuator/env`, `/actuator/heapdump`, `/metrics`, `/debug/vars`.
- Framework consoles: Laravel Ignition (`/_ignition/health-check`), Symfony profiler, Django debug tracebacks.
- `/.well-known/security.txt` for contact and policy hints.

## Phase 5: storage buckets

- Names from JS, HTML, DNS (`s3`, `blob`, `storage`, `cdn`), and the org name plus suffixes (`-backup`, `-dev`, `-assets`, `-prod`).
- AWS: `https://<bucket>.s3.amazonaws.com/` — `ListBucketResult` means public listing. Test regions from the error (`PermanentRedirect` names the region).
- Azure: `https://<account>.blob.core.windows.net/<container>?restype=container&comp=list`.
- GCP: `https://storage.googleapis.com/<bucket>`.
- Public listing with customer data is a finding; public read on an object referenced in the app is usually expected — say which.

## Phase 6: triage (the part that matters)

For each secret:

| Check | Question |
|---|---|
| format | does it match the provider's real shape (prefix, length, charset)? |
| liveness | one read-only call — AWS `aws sts get-caller-identity`, GitHub `/user`, Slack `auth.test` |
| scope | what can it reach, and does that match the task scope? |
| expiry | JWT `exp`, signed URL `X-Amz-Expires` |
| exposure path | was it public without auth? (yes = finding) |

Dead key in a public file = hardening note. Live key with in-scope read access = finding at the resulting capability level (usually high, critical if write/admin on production data).

## False-positive traps

- Placeholder secrets (`changeme`, `your-key-here`, example values).
- Public-by-design keys called leaks (Firebase apiKey, Stripe publishable).
- `.env` returning the SPA shell with 200.
- Source maps containing only vendored library code.
- A bucket that lists but holds only sample data.

## Tool and pacing table

| Tool | Use | Flags |
|---|---|---|
| curl | fetch, validate | one request in flight |
| git-dumper | .git recovery | `--jobs 1` |
| gitleaks, trufflehog | scan recovered trees | local |
| aws / az / gcloud | liveness and scope checks | single read-only call |

## Completion self-check

- [ ] every exposure cites a recovery URL and a body check (not status only).
- [ ] every live secret validated once, read-only, with the response kept.
- [ ] scope of each credential stated against `scope.md`.
- [ ] dead and placeholder material recorded as notes, not findings.
- [ ] repo dumped locally, target untouched.