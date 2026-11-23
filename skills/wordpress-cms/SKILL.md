---
name: wordpress-cms
description: "Invoke this skill when a target is WordPress or a common PHP CMS — version and plugin enumeration, user discovery, xmlrpc and REST abuse, backup and debug exposure, plugin CVE matching, and update-path gaps. Paced for shared hosting."
---
# WordPress and Common CMS

CMS targets are mostly a known-component problem: the app is standard, the plugins are not. Under an **authorized** engagement only. Work version-first, then component-specific, then app logic.

Shared hosting is fragile: one thread, and stop on 429.

## Hard gates

1. Identify the CMS before any enumeration; a non-WordPress site wastes a full run.
2. `wpscan` runs at `--max-threads 1`; a WPScan API token is optional, without it CVE mapping is manual.
3. No credential attack until the lockout policy is known (WordPress has none by default, but hosts and plugins add it).
4. Writes only through test accounts and test posts.

## Phase 1: confirm and version

- `/wp-login.php`, `/wp-admin/`, `/wp-json/`, `/?feed=rss2`, `readme.html`, `/license.txt`.
- Version from `?ver=` query strings, `/wp-includes/js/wp-emoji-release.min.js`, `readme.html`, and the REST index (`/wp-json/` namespaces).
- `curl -sS -D - "https://<host>/" | grep -iE "x-powered|link: .*wp-json"`.
- Record version and whether `readme.html` is reachable (it names the exact version).

## Phase 2: users

- `/?author=1` through `5` → 301 to `/author/<login>/` leaks the login.
- `/wp-json/wp/v2/users?per_page=100` — the REST API is a user oracle.
- oEmbed: `/wp-json/oembed/1.0/embed?url=https://<host>/?p=1`.
- Login error text differences at `/wp-login.php` (valid user vs not).
- Collect logins even without passwords; they feed `auth-and-session` later.

## Phase 3: components

```
wpscan --url "https://<host>/" --max-threads 1 --request-timeout 20 \
  --enumerate vp,vt,tt,cb,dbe --plugins-detection passive -o wpscan.txt
```

- Passive plugin detection first; aggressive (`--plugins-detection aggressive`) only if passive is empty and the target is stable.
- For every plugin/theme with a version: search the plugin slug plus version for public CVEs, then verify manually. A version banner alone is not a vulnerable install — many hosts patch in place.
- Themes: `/?theme=` in style.css, `wp-content/themes/<name>/style.css` version header.
- Check `wp-content/plugins/<slug>/readme.txt` for the changelog and version.

## Phase 4: exposure

- Backups: `wp-config.php.bak`, `wp-config.php~`, `.wp-config.php.swp`, `wp-content/backup*`, `wp-content/uploads/*.sql`, `*.zip`.
- Debug: `wp-content/debug.log` (frequent, high value — tokens, paths, SQL).
- Uploads listing: `/wp-content/uploads/` (`Index of`).
- `/wp-json/wp/v2/` full index for exposed post types, drafts, private meta.
- Registration open? `wp-login.php?action=register` — enumerate roles chosen at signup.

## Phase 5: endpoint abuse

- `xmlrpc.php`: `system.listMethods`, `wp.getUsersBlogs` (credential check without login form), `pingback.ping` (SSRF/port probe), brute-force amplification (many credentials per request).
- REST: `/wp-json/wp/v2/posts` POST with a test author; check capability checks per route.
- `wp-cron.php` reachable for compute amplification (note only).
- Admin-ajax actions: enumerate `action=` values in the front-end JS; `nopriv` handlers are the pre-auth surface.

## Phase 6: matching to impact

| Result | Impact |
|---|---|
| admin credentials | critical (theme/plugin editor → RCE) |
| plugin with unauth file upload/SQLi | critical/high, verify the CVE manually |
| debug.log with tokens | high |
| exposed backup with hashes | high (offline crack) |
| user enumeration only | low |

## False-positive traps

- Version from a `?ver=` on a core asset is often the WP version — confirm against `readme.html`/REST before quoting it.
- Plugin present but a fixed version (check changelog).
- `debug.log` 200 that is an HTML 404 page.
- `xmlrpc.php` present but disabled by the host (405/403 on POST).
- "Registration open" on a Multisite where signup needs approval.

## Tool and pacing table

| Tool | Use | Flags |
|---|---|---|
| wpscan | enumeration | `--max-threads 1 --request-timeout 20` |
| curl | manual checks, exposure | one request in flight |
| ffuf | backup/plugin slug discovery | `-rate 2 -t 1`, `/usr/share/wordlists/web/common.txt` |
| nuclei | WordPress CVE templates | `-rate-limit 2 -c 1 -tags wordpress` |

## Completion self-check

- [ ] CMS confirmed before enumeration.
- [ ] version corroborated from two independent sources.
- [ ] every plugin CVE verified against the installed version.
- [ ] exposure checks used the fixed list, no invented paths.
- [ ] credentials handed to `auth-and-session`, not brute-forced here without a policy.