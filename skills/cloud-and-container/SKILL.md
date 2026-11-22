---
name: cloud-and-container
description: "Invoke this skill when scope includes cloud accounts, object storage, container hosts or Kubernetes — key triage with read-only identity calls, bucket and metadata abuse, exposed Docker/K8s/etcd/kubelet APIs, and CI/CD service exposure. Read-only by default."
---
# Cloud and Container

Cloud scope is credentials and control planes, not pages. Under an **authorized** engagement only. Default posture is read-only: identity, list, describe. Writes only where the program allows and only on resources created for the test.

## Hard gates

1. Credentials from `secrets-and-exposure` are validated with **one** identity call per provider.
2. `sts get-caller-identity` first, always; it tells account, principal type and whether the key is live.
3. Bucket enumeration is HEAD/GET on the listing; no uploads.
4. Metadata access through SSRF goes one endpoint deep (`/latest/meta-data/`) unless the task needs more.
5. K8s: `get` only, no `apply`/`delete`; Docker: `ps`/`inspect`, no `run` unless stated.

## Phase 1: identity and scope of a key

```
aws sts get-caller-identity
aws iam get-user 2>/dev/null; aws iam list-attached-user-policies 2>/dev/null
az account show; az ad signed-in-user show
gcloud auth list; gcloud config list
```

- Root account, long-lived user key, or role session — record it.
- Enumerate what the principal can reach with the least output: `aws s3 ls`, `aws ec2 describe-regions`, `az group list`, `gcloud projects list`.
- Map permissions to a capability: read-only data, write data, IAM, compute. The mapping is the finding's severity.

Scoped enumeration by provider: `cloudfox aws --profile <p> all-checks` (or `cloudfox azure|gcp`), `scout aws` / `scout azure` / `scout gcp` for a full report into the work dir. Scout writes one HTML report; cloudfox writes JSON per check — both are read-only, but cloudfox fans out wide, so give it the account list first and one region at a time. `pacu` (in `/opt/pacu-venv`, wrapper `pacu`) holds a session per engagement and is the tool for exploit modules (`iam__privesc_scan`), not for the first pass.

## Phase 2: object storage

- AWS: `https://<bucket>.s3.amazonaws.com/` — `ListBucketResult` = public listing. Region from `PermanentRedirect`/`AuthorizationHeaderMalformed`.
- Azure blob: `?restype=container&comp=list`; account name from JS or DNS.
- GCP: `https://storage.googleapis.com/<bucket>?prefix=`.
- Test the four operations separately: list, get a known object, put (only if allowed), delete.
- Customer data in a public listing is the impact; sample 2-3 objects and note count.

## Phase 3: metadata service (via SSRF)

- AWS IMDSv1: `http://169.254.169.254/latest/meta-data/iam/security-credentials/` → role name → append it for temporary keys.
- IMDSv2 needs a token PUT — most SSRF clients cannot; note it as a limit rather than claiming failure.
- Azure: `http://169.254.169.254/metadata/instance?api-version=2021-02-01` with `Metadata: true`.
- GCP: `http://metadata.google.internal/computeMetadata/v1/` with `Metadata-Flavor: Google`.
- Temporary keys are live for ~1h; validate one with `sts get-caller-identity` and note the expiry.

## Phase 4: container hosts

- Docker API: `:2375/version`, `:2375/containers/json`, `:2376` with TLS. `:2375` open = effectively root on the host.
- Registry: `:5000/v2/_catalog`, `_catalog` with `n=100`.
- Escape paths to note, not perform: privileged container + mounted `docker.sock`, host path mounts.

## Phase 4b: from a pod, inside

Run `peirates` on the compromised pod/host — it auto-detects the cloud, the API server and the service-account token, then offers read-only modules (`-m` runs one): token review, `can-i`, secret dump, cloud metadata. `kube-bench` scores the node against the CIS benchmark (`kube-bench run --targets master,node` when the host filesystem is visible, `--json` for the report); it is the evidence for "cluster hardening gap", not a foothold.

## Phase 5: Kubernetes

- API server: `:6443/version`, `:6443/api`, `:8080` (insecure port, older clusters).
- Anonymous auth: `kubectl --server=https://<host>:6443 --insecure-skip-tls-verify get pods -A`.
- Kubelet: `:10250/pods`, `:10250/run/<ns>/<pod>/<container>` (POST, exec — a write; ask first).
- etcd: `:2379/v2/keys/?recursive=true` or `/v3/kv/range` with a base64 key.
- Dashboards: `/api/v1/namespaces/kube-system/services/https:kubernetes-dashboard:/proxy/`.
- Service-account tokens in pod env or mounted secrets give the cluster role; map it with `kubectl auth can-i --list`.

## Phase 6: CI/CD surfaces

- Jenkins `/script` (Groovy console), `/asynchPeople/`, `/api/json`; unauthenticated script console is RCE.
- GitLab `/api/v4/projects?visibility=public`, `/-/jobs/artifacts/`.
- GitHub Actions artifacts and logs (public repos) for tokens.
- Build config leaks: `.gitlab-ci.yml`, `Jenkinsfile`, `docker-compose.yml` in exposed repos or `.git`.
- Secrets in CI variables echoed into build logs.

## Severity anchors

| Finding | Level |
|---|---|
| metadata creds with in-scope read | high |
| public bucket with customer data | high/critical |
| unauth Docker 2375 | critical |
| anonymous K8s API with pod create | high/critical |
| unauth Jenkins script console | critical |
| public bucket with sample assets only | low |

## False-positive traps

- Keys that are dead (identity call fails) but look valid.
- Bucket listing that only allows `ListBucket` on a prefix with nothing in it.
- `:2376` open but client certs required.
- K8s API reachable but `system:anonymous` has no RBAC.
- Metadata endpoint reachable but only returns instance-id.

## Tool and pacing table

| Tool | Use | Flags |
|---|---|---|
| aws / az / gcloud | identity, enumeration | one call at a time |
| curl | buckets, metadata, APIs | one in flight |
| kubectl | read-only cluster checks | `--request-timeout=20s` |
| trivy | image and config scanning | local |
| cloudfox | scoped, read-only enum per check | one region/account at a time, `--profile` |
| scout | full report, read-only | `--max-workers 1` on the quiet profile |
| pacu | exploit modules per session | `/opt/pacu-venv/bin/pacu` |
| peirates | pod-side escape/enum | `-m <module>` for one run |
| kube-bench | CIS scoring | `--json` |

## Completion self-check

- [ ] identity established before any enumeration.
- [ ] every credential's scope written down.
- [ ] storage tested per operation, sample objects noted.
- [ ] no writes unless the policy allowed them.
- [ ] cloud and container findings separated from web findings.