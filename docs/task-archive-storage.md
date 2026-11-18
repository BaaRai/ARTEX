# Task archive storage

Task archives use local disk by default. The optional S3-compatible backend moves
cold task packages to object storage while retaining Archive, Restore and Delete.
Active workspaces and PostgreSQL remain local. This is task cold storage, not a
full-installation backup; a recovery plan must preserve the database, data
directory, bucket objects, and the credentials or roles needed to read them.

## Configuration

Use a private bucket and a separate prefix for each ARTEX installation. Configure
the bucket's default encryption to match the selected mode below, and enforce
that mode/key with a bucket policy. ARTEX does not provision or inspect bucket
encryption settings.

Set the following server environment and restart ARTEX:

```dotenv
ARTEX_ARCHIVE_STORAGE=s3
ARTEX_ARCHIVE_S3_ENDPOINT=https://s3.us-east-1.amazonaws.com
ARTEX_ARCHIVE_S3_REGION=us-east-1
ARTEX_ARCHIVE_S3_BUCKET=your-artex-archives
ARTEX_ARCHIVE_S3_PREFIX=artex/tasks
ARTEX_ARCHIVE_S3_SSE=AES256
```

With an EC2 instance role, ECS task role or other SDK-supported credential source,
no archive-specific access keys are needed.

| Variable | Meaning |
| --- | --- |
| `ARTEX_ARCHIVE_STORAGE` | `local` (default) or `s3`. |
| `ARTEX_ARCHIVE_S3_ENDPOINT` | Required HTTPS origin, without bucket/path/query. Loopback HTTP is allowed for local tests. |
| `ARTEX_ARCHIVE_S3_REGION` | Signing region; defaults to `us-east-1`. |
| `ARTEX_ARCHIVE_S3_BUCKET` | Required bucket name. |
| `ARTEX_ARCHIVE_S3_PREFIX` | Object prefix; defaults to `artex/tasks`. |
| `ARTEX_ARCHIVE_S3_SSE` | `AES256` (default, SSE-S3) or `aws:kms` (SSE-KMS). There is no unencrypted mode. |
| `ARTEX_ARCHIVE_S3_KMS_KEY_ID` | Optional KMS key ARN/ID/alias; valid only with `aws:kms`. Omit to use the provider's managed KMS key. |
| `ARTEX_ARCHIVE_S3_ACCESS_KEY_ID` | Optional dedicated credential override; must be supplied with the secret key. |
| `ARTEX_ARCHIVE_S3_SECRET_ACCESS_KEY` | Secret for the dedicated override. |
| `ARTEX_ARCHIVE_S3_SESSION_TOKEN` | Optional session token for dedicated temporary credentials. |
| `ARTEX_ARCHIVE_S3_PATH_STYLE` | `true` for path-style servers; otherwise virtual-hosted addressing is used. |

An invalid mode or partial credential override fails configuration instead of
silently falling back to local storage or ambient credentials. Preserve the
bucket, endpoint, prefix and read/delete access while remote archives remain.
Older local archives remain usable; they are not uploaded automatically.

## Encryption and permissions

Every single-object upload and multipart initiation explicitly requests the
configured server-side encryption. Upload verification also requires HEAD to
report the requested mode, and a KMS key identifier for SSE-KMS, before accepting
the object. Size and full SHA-256 readback remain mandatory; encryption metadata
does not replace content verification.

For SSE-KMS, set both the bucket's default encryption and ARTEX to `aws:kms`;
set `ARTEX_ARCHIVE_S3_KMS_KEY_ID` to the intended key when using a customer-managed
key. An explicit `AES256` request takes precedence over a bucket's KMS default,
so do not leave ARTEX on SSE-S3 when the bucket is intended to use KMS.

The principal needs object upload, read (HEAD and ranged GET), delete and
multipart upload/abort permissions. KMS uploads/readback also need the applicable
key-policy and IAM permissions for `kms:GenerateDataKey` and `kms:Decrypt`.
Retain old KMS keys and decrypt permissions when changing the key or mode; restore
does not require an older object to use the current upload policy.

Compatible providers must support the requested SSE mode and its HEAD metadata,
plus conditional ranged reads. A provider that ignores the encryption request
or cannot confirm it is rejected; the original task is retained. Local source and
temporary files keep their existing local-disk protection and restricted modes.

See the official [PutObject encryption and permission contract](https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutObject.html)
and [HEAD encryption response rules](https://docs.aws.amazon.com/AmazonS3/latest/API/API_HeadObject.html).

## Credential sources

Without the archive-specific override, ARTEX uses the AWS SDK default credential
chain, including standard AWS environment variables, shared profiles, web
identity, ECS task credentials and EC2 instance credentials through IMDS. Role
credentials retain the SDK's automatic refresh behavior. A named shared profile
can be selected with `AWS_PROFILE`.

For an external S3-compatible provider, dedicated keys can be supplied through
the archive-specific variables. Both key fields are required; a session token
is supported for temporary credentials. This explicit override takes precedence
over ambient AWS credentials. Operator-supplied temporary credentials must be
rotated by the operator; they are not an IAM role provider.

Keep secrets in a protected service environment file outside the repository and
frontend. A systemd `EnvironmentFile=` can point to a root-owned mode-0600 file.
See the [AWS SDK credential-chain documentation](https://docs.aws.amazon.com/sdk-for-go/v2/developer-guide/configure-gosdk.html).

## Lifecycle

1. Pause or finish a task and use its **Archive** action.
2. ARTEX stages the source files and builds a local `.tar.zst` package. Large
   packages use multipart upload, initially 8 MiB parts with two upload workers;
   the SDK increases the part size for exceptionally large seekable files.
3. ARTEX confirms encryption metadata and reads the complete object back,
   checking size and SHA-256. ETags and uploaded checksum metadata alone are
   insufficient.
4. **Only after verification and a successful PostgreSQL commit** does ARTEX
   remove the local package and staged source files. The committed archive
   location is an `s3://` URI; there is no schema or archive-format migration.
5. **Restore** downloads and verifies before installing files and database rows.
   The remote package is consumed only after restoration commits.
6. **Delete** commits removal of the archive stub, then cleans up the object.
   A durable journal retains failed cleanup work.

The archive list identifies **Object storage** and **Local storage** and shows
upload/download verification phases. Reads use bounded 8 MiB ranges pinned to the
object's ETag, with retries for interrupted ranges rather than the entire object.

## Interruption recovery

Source staging and restore journals live with task archives. Remote cleanup
journals live under `data/archives/tasks/.remote`; they contain locations, not
credentials. Keep this directory with PostgreSQL and the installation data.

| Interruption | Recovery |
| --- | --- |
| During staging/compression, before an upload journal exists | Roll staged source files back. Remove any temporary package only after rollback succeeds. The task remains in PostgreSQL. |
| After the upload journal, before successful verification | Roll source files back and clean up any uncommitted object/package. Failed cleanup retains its journal for retry. |
| After verification, before the database commit | Treat the object as uncommitted: retain the original task and clean up the orphan object. Verification alone never authorizes local deletion. |
| After the database commit, before package/staging cleanup | Keep the committed object and finish local cleanup. The persisted location and checksum establish commit, even if Restore or Delete has since been queued. |
| Download/extraction before restored files are installed | Remove temporary extraction data; keep the remote object and archive stub for another attempt. |
| Restored files installed, before database restoration commits | Roll back installed files using the restore journal. Retain the remote package. |
| Database restoration committed, before remote cleanup | Recognize the already-restored task and finish cleanup without replaying database rows. |
| Object consumed, before the restored archive stub is removed | Skip download/replay; repeat idempotent object deletion and remove the stub. |
| Delete journal written, before database deletion commits | Keep the object while its archive stub exists. The queued Delete operation can retry. |
| Database deletion committed, before object deletion succeeds | The durable outbox retains the URI and retries object deletion. |
| An old failed upload survives a retry using local storage | Remove the abandoned remote object, but retain a local package referenced by the committed archive row. |

The staging recovery rule also applies to local archives. Dedicated tests cover
both storage modes with ready, restore-queued, delete-queued and uncommitted
states. Additional PostgreSQL/HTTP-S3 tests reconstruct interrupted operations at
13 persisted boundaries and repeat recovery to check idempotence.

Handled multipart-upload failures are aborted by the SDK. Abrupt process death
may leave unfinished multipart uploads; configure bucket lifecycle expiration
for those parts. Existing object versions, if versioning is enabled, are governed
by the bucket's version-retention policy.

## Disk and transfer requirements

Archive creation still requires temporary local space for the compressed
package. Restore requires space for the download and extracted files. Full
upload readback and restores incur storage reads and applicable transfer charges.
Failed remote cleanup does not prevent unrelated local archive jobs from being
processed once the failed cleanup request returns.

## Optional connectivity check

Run `go run ./cmd/artex-archive-check` with the archive environment to upload,
verify, download and delete a small unique object. It performs real bucket
operations but does not touch task data, and reports cleanup failures.

Automated tests use HTTP fixtures for S3 and metadata credentials, plus isolated
PostgreSQL. They do not establish compatibility with a live cloud bucket.
