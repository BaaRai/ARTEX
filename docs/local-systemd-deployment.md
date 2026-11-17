# Local ARTEX systemd deployment

## Current deployment
- Checkout and executable: `/home/parallels/ARTEX`, `/home/parallels/ARTEX/artex`.
- Runtime user/group: `parallels` (not root).
- Database: PostgreSQL cluster `18/main`, `127.0.0.1:5432`, database/user `artex`.
- Configuration: `config.json`, ignored by Git, owner-only mode `0600`.
- Web UI: http://127.0.0.1:8787; recording proxy: `127.0.0.1:8788`.
- Unit source: `artex.service`; installed at `/etc/systemd/system/artex.service`.

## Database and credentials
PostgreSQL 18 was already installed. A dedicated non-superuser login role and database named `artex` were created. A generated database password was saved in `config.json`; unrelated configuration fields were preserved. The role has no role-creation or database-creation privileges.

On a fresh equivalent host, install PostgreSQL 18, confirm its cluster name using `pg_lsclusters`, and create the role/database only if absent:

```bash
sudo systemctl start postgresql@18-main.service
sudo -u postgres createuser --pwprompt artex
sudo -u postgres createdb --owner=artex artex
```

Enter a unique password at the prompt. Set `database.host` to `127.0.0.1`, `port` to `5432`, `user` and `dbname` to `artex`, `password` to that secret, and `sslmode` to `disable` in `config.json`. Disabling TLS is limited here to loopback traffic. Preserve other settings. Restrict access:

```bash
chmod 600 /home/parallels/ARTEX/config.json
```

`ARTEX_PG_DSN`, if set in systemd overrides, takes precedence over the file. Never commit configuration, credentials, encryption/signing keys, or startup logs.

## Build and install
The binary already includes the UI. To rebuild the UI, run `npm ci` and `npm run build:static` in `web/`. Create the missing parent with `mkdir -p server/webui`, then replace `server/webui/dist` with `web/out`. Build the binary with:

```bash
CGO_ENABLED=0 go build -tags embedui -trimpath -o artex ./cmd/artex
```

For a running deployment, build into a separate staging path and stop the service before replacing the executable or generated assets. Avoid rerunning `install.sh` without reviewing its prompts: it regenerates `config.json` and starts a separate process.

Review paths, user/group, and PostgreSQL cluster in the unit before installing on another host:

```bash
cd /home/parallels/ARTEX
systemd-analyze verify "$PWD/artex.service"
sudo install -o root -g root -m 0644 artex.service /etc/systemd/system/artex.service
sudo systemctl daemon-reload
```

If ARTEX is running outside systemd, identify its PID/executable and send that verified process SIGTERM. Wait for shutdown and release of ports 8787/8788 before enabling the service:

```bash
ss -ltnp '( sport = :8787 or sport = :8788 )'
sudo systemctl enable --now artex.service
systemctl status artex.service --no-pager
curl --fail --silent --show-error http://127.0.0.1:8787/api/health
```

The unit runs the binary directly, without the redundant `start.sh` supervisor. It requires and starts after `postgresql@18-main.service`, restarts on failure after five seconds, allows 30 seconds for graceful shutdown, logs to the journal, and uses `UMask=0077`. Five starts within 60 seconds trigger the start limit.

Because ARTEX explicitly requires the PostgreSQL cluster unit, the general `postgresql.service` unit need not be enabled separately. Cluster startup policy `/etc/postgresql/18/main/start.conf` is currently `auto` and must not be `disabled`.

## Web setup
On a new database, visit `/setup`, provide the token from the private startup log or journal, and choose a unique password of at least 12 characters. The username is `ARTEX`.

Setup on this host is already complete. Its generated login password is in an owner-only file under `/home/parallels/.local/state/artex/`; move it into your password manager. Browser verification covered setup, fresh login, Tasks, Assets, System Settings, and setup redirection after initialization. No scans or agent tasks were launched.

## Actual reboot verification
Pre-reboot checks confirmed persistent ARTEX enablement, active PostgreSQL and ARTEX services, HTTP 200 health, and initialized authentication. This is not proof of survival across reboot.

The pre-reboot boot ID and timestamp are saved in the ignored runtime file `data/reboot-verification-baseline.json`. Do not replace this baseline until the current reboot test is complete.

Save other work before rebooting; this disconnects local/remote sessions:

```bash
sudo systemctl reboot
```

After reconnecting, do not manually start ARTEX before checking automatic startup. Compare the current boot ID with the saved baseline and check the services:

```bash
cd /home/parallels/ARTEX
python3 -c 'import json; from pathlib import Path; old=json.loads(Path("data/reboot-verification-baseline.json").read_text())["boot_id"]; new=Path("/proc/sys/kernel/random/boot_id").read_text().strip(); print("PASS: a reboot occurred" if old != new else "FAIL: no reboot occurred"); raise SystemExit(0 if old != new else 1)'
systemctl is-enabled artex.service
systemctl is-active postgresql@18-main.service artex.service
curl --fail --silent --show-error http://127.0.0.1:8787/api/health
python3 -c 'import json, urllib.request; r=json.load(urllib.request.urlopen("http://127.0.0.1:8787/api/auth/status", timeout=5)); print("Authentication initialized:", r.get("initialized")); raise SystemExit(0 if r.get("initialized") else 1)'
systemctl show artex.service --no-pager -p ActiveState -p SubState -p MainPID -p ActiveEnterTimestamp
```

Success requires a changed boot ID, enabled ARTEX, both services active without manual startup, HTTP 200, and authentication still initialized. A new PID alone is not proof of reboot. If the system has just booted, allow startup time and repeat health checks. Record the observed results rather than assuming enablement proves persistence.

For a future independent reboot test, refresh the baseline with the current boot ID and timestamp before rebooting.

## Operations and troubleshooting
```bash
sudo systemctl restart artex.service
sudo systemctl stop artex.service
sudo systemctl start artex.service
journalctl -b -u artex.service --no-pager
journalctl -u artex.service -f
```

Treat journal output as potentially sensitive; never share unredacted setup tokens or credentials. If startup fails, check PostgreSQL status, database settings and permissions, occupied ports, and the journal. After a PostgreSQL dependency failure, restart ARTEX explicitly once the cluster is healthy. To recover from the start limit:

```bash
sudo systemctl reset-failed artex.service
sudo systemctl start artex.service
```

Back up PostgreSQL, configuration, runtime data, and encryption/signing keys securely before upgrades. The application stays loopback-only; remote access requires a separately reviewed tunnel or reverse proxy.

## Verified reboot result: 2026-11-17
Post-reboot verification completed at 2026-11-17 13:16:42 UTC over SSH, without manually starting or restarting either service.

- Current boot ID differed from the saved pre-reboot baseline: an actual reboot was confirmed.
- `artex.service` remained enabled and was active/running as `parallels`.
- systemd reported startup at 2026-11-17 13:15:50 UTC, PID 1582, with `NRestarts=0` at verification time.
- `postgresql@18-main.service` was active.
- `/api/health` returned HTTP 200.
- `/api/auth/status` confirmed initialized authentication was preserved.

Result: automatic ARTEX startup and persistent database-backed setup were verified across a real system reboot. PID and restart count above are observations from this verification, not fixed configuration values.
