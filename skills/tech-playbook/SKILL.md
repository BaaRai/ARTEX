---
name: tech-playbook
description: "Infrastructure and platform targets: cloud (AWS, Firebase, Supabase), containers and Kubernetes, CI/CD and source control, datastores (Redis, MongoDB, Elasticsearch, Memcached), web servers, observability stacks, mobile, and IoT firmware. Index of 25 references."
---

# Technology playbook (index)

Twenty-five references covering the platform layer under `references/`.

This is the pack with the widest gap between the method as written and what this
image can actually run. The methods are sound; the tooling often is not present.
Read the environment section before committing to a plan.

## Hard gates

1. **Cloud and platform scope is explicit or absent.** A discovered S3 bucket,
   Firebase project, or exposed Kubernetes API is not automatically in scope.
   Third-party or shared infrastructure is out of bounds unless named.
2. **Never touch other tenants.** In shared or multi-tenant services, prove
   exposure with a read of your *own* object or a metadata-only check. Enumerating
   or downloading other customers' data is out of scope by default and is an
   incident, not a finding.
3. **CI/CD and source control hold live secrets.** Reading a token to prove it is
   valid is the maximum step. Do not use it, do not push with it, and redact it
   in the report.
4. **Destructive infrastructure operations are forbidden** without explicit
   written permission: deleting buckets, scaling clusters, restarting services,
   modifying pipelines.
5. **Container and cluster findings need blast-radius language.** "Kubelet
   anonymous auth enabled" matters because of what it reaches. State the reach.

## Environment (important)

Present: `nmap`, `curl`, `jq`, `git`, Node 22, `python3`, `playwright-cli`,
`ripgrep`, `ffuf`, `httpx`, `nuclei`.

**Not installed:** `aws` CLI, `gcloud`, `az`, ScoutSuite, Pacu, CloudFox,
`trivy`, `kube-hunter`, `kube-bench`, `kubectl`, `docker`, `binwalk`, `qemu`,
`jadx`, `apktool`, `frida`, `ghidra`, `radare2`. Where a reference depends on
these, you can still: reach the management API with `curl` and prove
unauthenticated access, or inspect a downloaded artifact with `python3` and
`ripgrep`. Say explicitly in the finding which step you could not complete.

Mobile references are method-only here; dynamic instrumentation is not
available in this image.

## Choosing a reference

**Cloud**
- AWS enumeration — `references/aws-enum-scoutsuite.md`
- AWS exploitation — `references/aws-exploit-pacu.md`
- Multi-cloud enumeration — `references/cloud-enum-cloudfox.md`
- Cloud security baseline — `references/cloud-security.md`
- Firebase / Firestore rules — `references/firebase-firestore.md`
- Supabase / PostgREST — `references/supabase.md`

**Containers and orchestration**
- Docker daemon and containers — `references/docker-container.md`
- Kubernetes — `references/kubernetes-pentest.md`
- Kubelet, etcd, RBAC — `references/kubernetes-pentest.md`

**CI/CD and source control**
- Pipelines generally — `references/cicd-attacks.md`
- GitHub and GitLab — `references/gitlab-github.md`
- Jenkins — `references/jenkins.md`

**Datastores**
- Redis — `references/redis.md` · MongoDB — `references/mongodb.md`
- Elasticsearch — `references/elasticsearch.md`
- Memcached — `references/memcached.md`

**Servers**
- nginx / Apache — `references/nginx-apache.md`
- Apache misconfiguration — `references/apache-misconfig.md`
- Tomcat — `references/tomcat.md`

**Other platforms**
- Observability stacks (Prometheus, Grafana, Jaeger) — `references/observability-stack-attacks.md`
- Enterprise web estate — `references/enterprise-web-pentest.md`
- Mobile applications — `references/mobile-app-pentesting.md`
- Frida instrumentation — `references/frida-hooking.md`
- IoT firmware — `references/iot-firmware.md`
- QEMU emulation of firmware — `references/qemu-emulation.md`
- Configuration hardening — `references/config-hardening.md`

## Method

1. **Establish what the service is and who owns it** before probing. Ownership
   decides scope.
2. **Prefer read-only proof.** Unauthenticated `GET /` on an exposed dashboard
   proves exposure; downloading the data does not strengthen the finding.
3. **Check the default configuration** for each platform. Most of these classes
   are "the default is open, the vendor expects you to close it".
4. **Map reach, not just access.** A finding's severity is what it reaches.

## Boundaries

- Do not interact with another tenant's objects, even to check a rule.
- Do not use discovered CI credentials. Validity check only.
- If a platform finding requires `kubectl`, `aws` or `docker` to demonstrate
  impact, report the exposure and state the unverified impact explicitly rather
  than implying you confirmed it.
