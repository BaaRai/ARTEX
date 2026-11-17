# Task archive storage

Task archives use local disk by default. The optional S3-compatible backend moves
cold task packages to object storage after verifying their contents, releasing
local package space while retaining ARTEX's Archive, Restore and Delete actions.

Active workspaces and the live PostgreSQL database stay local. This feature is
task-level cold storage; use the existing backup workflow for a full installation
backup. A complete recovery plan must also preserve the bucket objects and
access to them.

## Configuration

Create a private bucket and a dedicated credential scoped to the archive prefix.
The backend needs object upload, read (including HEAD and byte ranges), delete,
and multipart upload/abort permissions. Use a separate prefix for each deployment.

Set these environment variables for the ARTEX server, then restart it:

```dotenv
ARTEX_ARCHIVE_STORAGE=s3
ARTEX_ARCHIVE_S3_ENDPOINT=https://s3.us-east-1.amazonaws.com
ARTEX_ARCHIVE_S3_REGION=us-east-1
ARTEX_ARCHIVE_S3_BUCKET=your-artex-archives
ARTEX_ARCHIVE_S3_PREFIX=artex/tasks
ARTEX_ARCHIVE_S3_ACCESS_KEY_ID=REPLACE
ARTEX_ARCHIVE_S3_SECRET_ACCESS_KEY=REPLACE
```

Store credentials in the service's protected environment file, outside the
repository and frontend. For systemd, a root-owned environment file with mode
0600 can be referenced through `EnvironmentFile=`.

| Variable | Meaning |
| --- | --- |
| `ARTEX_ARCHIVE_STORAGE` | `local` (default) or `s3`. |
| `ARTEX_ARCHIVE_S3_ENDPOINT` | Required HTTPS origin, without bucket/path/query. Loopback HTTP is allowed for local testing. |
| `ARTEX_ARCHIVE_S3_REGION` | Signing region; defaults to `us-east-1`. Match the provider's requirements. |
| `ARTEX_ARCHIVE_S3_BUCKET` | Required bucket name. |
| `ARTEX_ARCHIVE_S3_PREFIX` | Object prefix; defaults to `artex/tasks`. |
| `ARTEX_ARCHIVE_S3_ACCESS_KEY_ID` | Dedicated access key. |
| `ARTEX_ARCHIVE_S3_SECRET_ACCESS_KEY` | Dedicated secret key. |
| `ARTEX_ARCHIVE_S3_PATH_STYLE` | Set to `true` for compatible path-style servers; otherwise virtual-hosted addressing is used. |

For DigitalOcean Spaces, use its regional origin, such as
`https://sfo3.digitaloceanspaces.com`, and the provider's signing region. For a
local path-style test server, use an endpoint such as `http://127.0.0.1:9000` and
set `ARTEX_ARCHIVE_S3_PATH_STYLE=true`.

Invalid S3 configuration fails new archive jobs instead of silently falling back
to local disk. Preserve the configured bucket, endpoint, prefix and read/delete
access while remote archives remain. The same installation can still restore or
delete older local archives.

## Lifecycle and integrity

1. Pause or finish a task and use its existing **Archive** action.
2. ARTEX builds its `.tar.zst` package locally and uploads it. Large packages use
   multipart upload, starting with 8 MiB parts and two upload workers (the SDK
   increases the part size for exceptionally large seekable files).
3. ARTEX reads the entire object back and checks its size and SHA-256. Multipart
   ETags and uploaded checksum metadata alone are not treated as proof of content.
4. Only after verification does PostgreSQL commit the `s3://` location and
   compact the archived task. ARTEX then removes local source files and the
   temporary package. The archive list shows **Object storage** or **Local storage**.
5. **Restore** downloads and verifies the package before restoring task files and
   database records. The remote package is consumed after restoration commits.
   Failed remote cleanup can be retried without replaying restored records.
6. **Delete** removes the archive stub and queues durable cleanup of its remote
   object. Cleanup is retried after restart and before subsequent archive jobs.

Reads use bounded 8 MiB ranges pinned to the object's ETag. Interrupted ranges
are retried without duplicating output or restarting a multi-gigabyte download.
An object change, checksum mismatch or invalid range fails verification. Failed
downloads are removed, and upload/verification failures retain the original task.

There is no database schema or archive format migration. Existing local archive
packages remain compatible. Files outside ARTEX's task archive database are not
automatically imported or uploaded.

## Disk, transfer and recovery

Archive creation still needs space for the temporary compressed package.
Restoration needs space for the downloaded package and extracted files. Full
upload readback and later restores incur object-storage reads and any applicable
transfer charges. Configure expiration of abandoned multipart uploads for abrupt
process termination.

Recovery journals under `data/archives/tasks/.remote` contain object locations,
not credentials. Preserve them with PostgreSQL and the installation's data
directory. The worker uses these journals to clean up uploads that never
committed, keep committed objects, and finish deferred deletions. Failed remote
cleanup does not block unrelated local archive jobs.

## Optional connectivity check

Run `go run ./cmd/artex-archive-check` with the same archive environment as the
server to upload, verify, download and delete a small uniquely named test object.
This command performs real bucket operations but does not touch task data. It
reports leftover test objects if cleanup fails.

Automated tests use an HTTP S3 fixture and isolated PostgreSQL databases. They
cover multipart transfers, corruption, interrupted range retries, object changes,
cancellation, upload rollback, corrupted restores and persistent cleanup.
