---
name: supply-chain-ci
description: "Invoke this skill for dependency and build-pipeline risk — lockfile review against OSV, typosquat and dependency-confusion checks, CI workflow injection, artifact and cache poisoning, base-image age, and SBOM generation with trivy. Code and pipeline review, no live target requests required."
---
# Supply Chain and CI/CD

The dependencies and pipelines that build the target are attack surface the app never shows. Under an **authorized** engagement only; repository and CI access comes from the scope.

Most of this is offline review. Only registry and OSV lookups leave the box.

## Hard gates

1. Review the real lockfile, not the manifest: `package-lock.json`, `poetry.lock`, `go.sum`, `Gemfile.lock`, `pom.xml` with versions.
2. Confirm the ecosystem before querying OSV; a wrong ecosystem returns nothing and looks clean.
3. No version upgrades; the task is to report, not to modernise.
4. CI findings must name the file and line of the injectable construct.

## Phase 1: inventory and SBOM

```
trivy fs --scanners vuln,secret --format table .
trivy image <image>                     # when an image is in scope
```

- Also: `gitleaks detect --no-git -s .` and `trufflehog filesystem . --no-update`.
- Store the SBOM (`trivy fs --format cyclonedx -o sbom.json .`) as an artifact.
- Count direct versus transitive; transitive-only noise is separated from direct reachable packages.

## Phase 2: vulnerability matching

- Query OSV per package/version: `curl -sS -X POST https://api.osv.dev/v1/query -d '{"package":{"name":"<pkg>","ecosystem":"npm"},"version":"<ver>"}'`.
- Ecosystems: `npm`, `PyPI`, `Go`, `Maven`, `RubyGems`, `crates.io`, `Packagist`.
- Only vulnerabilities with a version range that includes the installed version count.
- Reachability: is the vulnerable function actually called? Grep the import and the call site. Unreachable = hardening note.
- Record CVE/GHSA id, fixed version, and the call site for every kept finding.

## Phase 3: dependency confusion and typosquats

- Private-scope packages without a registry scope pin (`@company/foo` with a public fallback) — check `.npmrc`, `pip.conf`, `nuget.config` for registry order.
- Typosquats: edit distance 1-2 against popular names in the lockfile, plus recently published versions of near-miss names.
- Install scripts from dependencies (`preinstall`, `postinstall`) that fetch or execute — note the ones in direct deps.
- Git URLs pinned to a branch instead of a commit sha.

## Phase 4: CI workflow injection

Read every workflow file (`.github/workflows/*.yml`, `.gitlab-ci.yml`, `Jenkinsfile`).

- `pull_request_target` + checkout of the PR head + execution of PR-controlled code = token exposure.
- Expression injection into `run:`: `${{ github.event.issue.title }}`, `${{ github.head_ref }}`, PR body — unquoted interpolation into a shell.
- `workflow_run` chains trusting artifacts from a lower-privilege workflow.
- Secrets in `env:` at workflow level reaching untrusted steps; `permissions: write-all`.
- Artifact download without digest verification; cache keys that cross trust boundaries (`key: ${{ github.ref }}` on a fork).
- Self-hosted runner labels: `pull_request` on a self-hosted runner executes fork code on internal infrastructure.

## Phase 5: base images and build config

- Dockerfile: `FROM` tag is `latest` or an old digest; no digest pin.
- `ADD` of a remote URL executed as root; `curl | sh` in the build.
- Build args carrying secrets (`ARG` + `ENV` persists into layers).
- `.dockerignore` missing, leaking `.env`/`.git` into the image.
- Image scan output: criticals in the base OS layer are the base image's problem — report against the base, not the app.

## Phase 6: report shape

| Finding | Severity clue |
|---|---|
| `pull_request_target` + PR code execution | high/critical |
| shell injection via PR title/ref | high |
| direct dep with reachable RCE/deserialization CVE | high |
| dependency confusion on a private scope | high |
| unpinned base image / `latest` | low/medium |
| transitive-only CVE, unreachable | hardening note |

## False-positive traps

- CVE range that excludes the installed version.
- Vulnerable package present but the vulnerable function never imported.
- `pull_request_target` without checkout of the head.
- Typosquat candidate that is simply the legitimate fork.
- Secret scanner hit on a placeholder or an example key.

## Tool and pacing table

| Tool | Use | Flags |
|---|---|---|
| trivy | vuln and SBOM | local |
| gitleaks / trufflehog | secret scan | local, `--no-git` |
| curl | OSV queries | serial |
| jq | SBOM and lockfile parsing | local |

## Completion self-check

- [ ] lockfiles reviewed, not manifests.
- [ ] every CVE checked against the installed range.
- [ ] reachability decided per kept finding.
- [ ] workflow injection cites file and line.
- [ ] SBOM stored as an artifact.