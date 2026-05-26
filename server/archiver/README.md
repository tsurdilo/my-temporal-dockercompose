# server/archiver

Status-filtered custom Temporal archival provider backed by MinIO. Wired into the server via
`temporal.WithCustomHistoryArchiverFactory` and `temporal.WithCustomVisibilityArchiverFactory`
in `server/main.go`.

## Table of contents

- [What it does](#what-it-does)
- [How status filtering works](#how-status-filtering-works)
- [Object key layout](#object-key-layout)
- [Files](#files)
  - [config.go](#configgo)
  - [client.go](#clientgo)
  - [keys.go](#keysgo)
  - [compress.go](#compressgo)
  - [history_archiver.go](#history_archivergo)
  - [visibility_archiver.go](#visibility_archivergo)
  - [factory.go](#factorygo)
- [Configuration](#configuration)
  - [allowedStatuses — controlling which executions are archived](#allowedstatuses--controlling-which-executions-are-archived)
- [Activating archival](#activating-archival)
- [Querying archived data](#querying-archived-data)
- [Known limitations](#known-limitations)
- [Testing archival locally](#testing-archival-locally)

---

## What it does

Archives closed workflow history and visibility records to **MinIO** (an S3-compatible object
store running as a Docker Compose service alongside the cluster). Before uploading anything,
both archivers check the workflow's terminal status against a configurable allowlist — only
matching executions are stored. Non-matching executions are silently skipped with zero network
I/O beyond the history read itself.

All uploaded objects are **gzip-compressed**. History is stored as a single object per
execution; visibility records are date-partitioned for easy lifecycle management.

The archiver registers the scheme **`minio://`**. Any URI with a different scheme falls through
to the built-in archivers unchanged.

---

## How status filtering works

### Visibility

`VisibilityRecord.Status` (`WorkflowExecutionStatus`) is a direct field on the record passed to
`Archive()`. The check is the first thing the archiver does — if the status is not in
`allowedStatuses`, the method returns `nil` immediately, before any encoding or network call.

### History

`ArchiveHistoryRequest` carries no status field. The history archiver must iterate through all
history blobs (via `HistoryIterator`) and derive the terminal status from the last event type
in the last blob (`Header.IsLast == true`). The mapping is:

| Last event type | Derived status |
|---|---|
| `EVENT_TYPE_WORKFLOW_EXECUTION_COMPLETED` | `Completed` |
| `EVENT_TYPE_WORKFLOW_EXECUTION_FAILED` | `Failed` |
| `EVENT_TYPE_WORKFLOW_EXECUTION_TIMED_OUT` | `TimedOut` |
| `EVENT_TYPE_WORKFLOW_EXECUTION_CANCELED` | `Canceled` |
| `EVENT_TYPE_WORKFLOW_EXECUTION_TERMINATED` | `Terminated` |
| `EVENT_TYPE_WORKFLOW_EXECUTION_CONTINUED_AS_NEW` | `ContinuedAsNew` |

All blobs are accumulated in memory during iteration. If the derived status is in the allowlist,
the complete buffer is encoded, compressed, and uploaded as a single object. If not allowed, the
buffer is discarded and `nil` is returned. History reads are consumed either way — this is
unavoidable without a server-side change to `ArchiveHistoryRequest`.

---

## Object key layout

### History

```
{uriPath}/{namespaceID}/{workflowID}/{runID}_{closeFailoverVersion}.history.gz
```

Example (`minio://temporal-history/history` URI):
```
history/default-ns-id/my-workflow-id/run-abc123_0.history.gz
```

No date partition. `Get()` reconstructs this key exactly from the four fields available in
`GetHistoryRequest`: `NamespaceID`, `WorkflowID`, `RunID`, `CloseFailoverVersion`. Date
partitioning is impossible because there is no close timestamp in `GetHistoryRequest`.

### Visibility

```
{uriPath}/{namespaceID}/{YYYY}/{MM}/{DD}/{closeTimeUnixNano}_{shortRunID}.visibility.gz
```

Example:
```
visibility/default-ns-id/2026/05/26/1748285234000000000_run-abc1.visibility.gz
```

Date-partitioned by close time (from `VisibilityRecord.CloseTime`). Listing by prefix
`visibility/{namespaceID}/` gives time-ordered results; MinIO lifecycle rules can target a
specific date range with `visibility/{namespaceID}/2026/` as the prefix.

---

## Files

### `config.go`

Defines the `Config` struct and `ParseConfig(map[string]any) (*Config, error)`.

`ParseConfig` is called by both factories at archiver construction time. It reads the following
keys from the `customStores.minio` YAML block (all required except `allowedStatuses`):

| YAML key | Type | Description |
|---|---|---|
| `endpoint` | string | MinIO S3 API endpoint, e.g. `http://minio:9000` |
| `region` | string | AWS region name — MinIO ignores the value but the SDK requires it; use `us-east-1` |
| `accessKeyID` | string | MinIO root user or access key |
| `secretAccessKey` | string | MinIO root password or secret key |
| `allowedStatuses` | list of strings | Statuses to archive. Valid values: `Completed`, `Failed`, `TimedOut`, `Canceled`, `Terminated`, `ContinuedAsNew`. Omit or leave empty to archive all statuses. |

`Config.StatusAllowed(status)` returns `true` if the status should be archived (always `true`
when `allowedStatuses` is empty).

### `client.go`

`NewS3Client(cfg *Config) (*s3.Client, error)` — constructs an `aws-sdk-go-v2` S3 client
configured for MinIO:

- **Static credentials** — bypasses EC2 metadata, env-var, and profile credential chain;
  uses only `accessKeyID` / `secretAccessKey` from `Config`
- **`BaseEndpoint`** — overrides the AWS endpoint to point at the MinIO server
- **`UsePathStyle = true`** — required by MinIO; it does not support virtual-hosted-style
  bucket addressing (`bucket.endpoint` is not valid for MinIO)

### `keys.go`

Two pure key-construction functions and one prefix helper — no I/O:

- `HistoryKey(uriPath, namespaceID, workflowID, runID string, closeFailoverVersion int64) string`
- `VisibilityKey(uriPath, namespaceID, runID string, closeTime time.Time) string`
- `VisibilityPrefix(uriPath, namespaceID string) string` — prefix used by `Query()` to list
  all visibility objects for a namespace

`uriPath` in all three functions is `uri.Path()` from the archival URI (e.g. `/history` for
`minio://temporal-history/history`). The leading `/` is stripped before use.

### `compress.go`

Two thin wrappers around `compress/gzip` — no third-party dependency:

- `GzipCompress(data []byte) ([]byte, error)` — `gzip.BestSpeed` level
- `GzipDecompress(data []byte) ([]byte, error)`

Used by both archivers on every upload and download.

### `history_archiver.go`

Implements `archiver.HistoryArchiver`.

**`ValidateURI(uri)`** — checks scheme is `"minio"` and hostname (bucket name) is non-empty.

**`Archive(ctx, uri, request, opts...)`**:
1. Validates URI and request
2. Builds a `HistoryIterator`, restoring heartbeat state from a previous activity attempt if
   available via `featureCatalog.ProgressManager`
3. Iterates blobs, accumulating `historyBlob.Body` (`[]*historypb.History`) into a buffer;
   heartbeats iterator state on every blob so the activity doesn't time out
4. On the blob where `Header.IsLast == true`: derives terminal status from the last event type,
   checks `historyMutated` (failover version / event ID sanity check), applies the status filter
5. If allowed: `codec.JSONPBEncoder.EncodeHistories` → `GzipCompress` → `s3.PutObject`
6. If not allowed: returns `nil` (buffer discarded, nothing uploaded)

**`Get(ctx, uri, request)`**:
1. Reconstructs the object key from request fields
2. `s3.GetObject` → `GzipDecompress` → `codec.JSONPBEncoder.DecodeHistories`
3. Paginates the decoded `[]*historypb.History` slice using `request.PageSize` and
   `request.NextPageToken` (single-byte token encoding the start index)

`NoSuchKey` S3 errors are mapped to `serviceerror.NewNotFound`; other S3 errors to
`serviceerror.NewUnavailable`.

### `visibility_archiver.go`

Implements `archiver.VisibilityArchiver`.

**`ValidateURI(uri)`** — same check as history: scheme `"minio"`, non-empty hostname.

**`Archive(ctx, uri, request, opts...)`**:
1. Validates URI and request
2. **Status filter first** — `cfg.StatusAllowed(request.GetStatus())` before any encoding
3. `codec.JSONPBEncoder.Encode(request)` → `GzipCompress` → `s3.PutObject`

Key uses `VisibilityKey(uri.Path(), namespaceID, runID, closeTime)` where `closeTime` is
`request.GetCloseTime().AsTime()`.

**`Query(ctx, uri, request, saTypeMap)`**:
1. Lists objects under `VisibilityPrefix(uri.Path(), namespaceID)` using `s3.ListObjectsV2`
2. Downloads, decompresses, and decodes each object
3. Converts each `VisibilityRecord` to `WorkflowExecutionInfo` via `convertToExecutionInfo`
4. Stops once `request.PageSize` results are collected
5. Returns `NextPageToken` as the raw S3 continuation token when the result set is truncated

**`parseStatusFilter(query)`** — parses `ExecutionStatus = 'X'` from `request.Query`
(case-insensitive, single or double quotes). Returns the matching `WorkflowExecutionStatus`
enum value, a boolean indicating whether a filter was found, and an error if the status name
is unrecognised.

`Query()` loops over S3 batches (over-fetching 5× `PageSize` when a status filter is active)
until `PageSize` matching records are collected or all objects in the namespace prefix are
exhausted. Non-matching records are skipped after download; the S3 `NextContinuationToken`
is carried forward as `NextPageToken` when more objects remain.

### `factory.go`

Two factory functions that satisfy the `provider.CustomHistoryArchiverFactory` and
`provider.CustomVisibilityArchiverFactory` interfaces using the `FactoryFunc` adapter types:

```go
func NewHistoryArchiverFactory() provider.CustomHistoryArchiverFactory
func NewVisibilityArchiverFactory() provider.CustomVisibilityArchiverFactory
```

Both functions return `provider.ErrUnknownScheme` for any scheme that is not `"minio"`, which
causes `ArchiverProvider` to fall through to the built-in archivers (filestore, s3, gcloud)
unchanged. When the scheme is `"minio"`, they call `ParseConfig(params.Configs)` and construct
the archiver.

---

## Configuration

The `customStores.minio` block lives in the cluster's config template at:
```
my-temporal-dockercompose/template/my_config_template.yaml
```

This file is mounted into every Temporal service container at
`/etc/temporal/config/config_template.yaml` and loaded via the
`TEMPORAL_SERVER_CONFIG_FILE_PATH` env var (set in `compose-services.yml`).
It is rendered when `USE_MINIO_ARCHIVAL` is set to a non-empty value:

```yaml
archival:
  history:
    state: "enabled"
    enableRead: true
    provider:
      customStores:
        minio:
          endpoint: "http://minio:9000"
          region: "us-east-1"
          accessKeyID: "minioadmin"
          secretAccessKey: "minioadmin"
          allowedStatuses: []   # empty = archive all statuses; add e.g. "Failed", "Terminated" to restrict
  visibility:
    state: "enabled"
    enableRead: true
    provider:
      customStores:
        minio:
          endpoint: "http://minio:9000"
          region: "us-east-1"
          accessKeyID: "minioadmin"
          secretAccessKey: "minioadmin"
          allowedStatuses: []   # empty = archive all statuses; add e.g. "Failed", "Terminated" to restrict

namespaceDefaults:
  archival:
    history:
      state: "enabled"
      URI: "minio://temporal-history/history"
    visibility:
      state: "enabled"
      URI: "minio://temporal-visibility/visibility"
```

### `allowedStatuses` — controlling which executions are archived

`allowedStatuses` is a list of terminal `WorkflowExecutionStatus` values. Only workflows that
close with one of the listed statuses are written to MinIO. Workflows with any other status are
silently skipped — no object is written, no error is returned.

**Archive all statuses (default):**
```yaml
allowedStatuses: []
```
An empty list disables the filter entirely — every closed execution is archived regardless of
its terminal status.

**Archive only specific statuses:**
```yaml
allowedStatuses:
  - "Failed"
  - "TimedOut"
  - "Terminated"
```

Valid values (case-sensitive):

| Value | Meaning |
|---|---|
| `Completed` | Workflow returned successfully |
| `Failed` | Workflow returned an application error |
| `TimedOut` | Workflow exceeded its execution timeout |
| `Canceled` | Workflow was explicitly canceled |
| `Terminated` | Workflow was forcibly terminated |
| `ContinuedAsNew` | Workflow continued as a new run |

The filter is evaluated independently for history and visibility. Both blocks have their own
`allowedStatuses` key — you can archive visibility records for all statuses while only
persisting history for failures, or any other combination.

After changing `allowedStatuses`, rebuild all server images and restart the stack:

```bash
docker compose -f compose-postgres.yml -f compose-services.yml down
docker compose -f compose-postgres.yml -f compose-services.yml build \
  temporal-history temporal-history2 \
  temporal-matching temporal-matching2 \
  temporal-frontend temporal-frontend2 \
  temporal-internal-frontend \
  temporal-worker
docker compose -f compose-postgres.yml -f compose-services.yml up --detach
```

The change only affects new archival writes — objects already in MinIO are not modified.

---

The `customStores.minio` map is what `ArchiverProvider` delivers as `params.Configs` to the
factory function. The `URI` fields determine the bucket (`uri.Hostname()`) and key prefix
(`uri.Path()`) for each archiver independently.

---

## Activating archival

MinIO archival is **enabled by default** (`USE_MINIO_ARCHIVAL=true` in `.env`). To disable it,
set `USE_MINIO_ARCHIVAL=false` in `.env` before starting the stack.

On first use (or after any change to `server/`), rebuild all server images:

```bash
docker compose build \
  temporal-history temporal-history2 \
  temporal-matching temporal-matching2 \
  temporal-frontend temporal-frontend2 \
  temporal-internal-frontend \
  temporal-worker
docker compose up -d
```

MinIO starts first → `minio-init` creates the two buckets → Temporal services start
(`temporal-history`, `temporal-history2`, and `temporal-worker` have `depends_on: minio-init`).
The `temporal-admin-tools` setup container then patches the `default` namespace to enable
archival URIs via `temporal operator namespace update`.

The `minio` console is accessible at `http://localhost:9011` (credentials: `minioadmin` /
`minioadmin`). The S3 API is at `http://localhost:9010`.

---

## Querying archived data

### List archived workflow executions

```bash
# all archived workflows
temporal workflow list --archived --namespace default

# filter by execution status
temporal workflow list --archived --namespace default --query "ExecutionStatus = 'Failed'"
temporal workflow list --archived --namespace default --query "ExecutionStatus = 'Terminated'"
temporal workflow list --archived --namespace default --query "ExecutionStatus = 'Completed'"
temporal workflow list --archived --namespace default --query "ExecutionStatus = 'TimedOut'"
temporal workflow list --archived --namespace default --query "ExecutionStatus = 'Canceled'"
temporal workflow list --archived --namespace default --query "ExecutionStatus = 'ContinuedAsNew'"
```

`ExecutionStatus` filtering is handled client-side in `Query()`: all objects in the namespace
prefix are fetched in batches and records that don't match the status are discarded before
being returned. The match is exact — only the values listed above are recognised (case-insensitive).

Any other query clauses (WorkflowType, date ranges, etc.) are silently ignored.
See [Known limitations](#known-limitations).

### Retrieve the full history of an archived workflow

```bash
temporal workflow show --workflow-id <id> --run-id <run-id> --namespace default
```

When the workflow is no longer in primary persistence, the history service falls back to the
history archiver's `Get()` method automatically. No special flag is needed.

### Does the CLI receive compressed data?

No. Decompression is transparent and happens entirely inside the archiver before the data
leaves the server process. The call path for `workflow show` on an archived execution is:

```
CLI → frontend gRPC → history service
                          └─ not in persistence → HistoryArchiver.Get()
                                └─ s3.GetObject  (raw .gz bytes from MinIO)
                                └─ GzipDecompress()         ← archiver internals
                                └─ codec.DecodeHistories()  ← archiver internals
                                └─ returns []*historypb.History
                          └─ returns GetWorkflowExecutionHistoryResponse
CLI ← receives normal history events, identical to a live workflow
```

The same applies to `workflow list --archived`: `Query()` decompresses each visibility record
before converting it to `WorkflowExecutionInfo`. The CLI, SDK, and UI are all unaware that
gzip is involved.

---

## Known limitations

- **ExecutionStatus filtering only** — `Query()` supports filtering by `ExecutionStatus = 'X'`
  (client-side, after downloading each record). Other SQL-like clauses (`WorkflowType`,
  `WorkflowId`, date ranges) are silently ignored and return all records unfiltered.
- **Single-object history** — all history for one execution is stored as a single compressed
  object. No multipart upload. Fine for a dev cluster; large production executions with many
  thousands of events would benefit from chunking.
- **No TLS** — MinIO is configured for plain HTTP. Suitable for local development only.
- **Hardcoded credentials** — `minioadmin` / `minioadmin` are baked into the config template.
  Use MinIO access key management and inject credentials via env vars before any production use.
- **No lifecycle rules** — object expiry / tiering must be configured separately via
  `mc ilm add` or the MinIO console.
- **History pagination token** — the `NextPageToken` for `Get()` is a single byte encoding
  the batch-slice start index, which limits pagination to 255 page turns. Sufficient for dev
  use; replace with a proper serialized token for production.

---

## Testing archival locally

By default the cluster runs with compiled-in server values:

| What | Config key | Default value |
|---|---|---|
| Delay before the archival queue fires after close | `history.archivalProcessorArchiveDelay` | 5 min (with full jitter, so 0–5 min) |
| Minimum retention you can set on a local namespace | `system.namespaceMinRetentionLocal` | 1 h |
| Namespace retention for `default` | per-namespace | 72 h (server default) |

These are intentionally left at their normal values. To test archival without waiting, lower them temporarily with the commands below. All changes are live via etcd watch — no restart needed.

### Step 1 — Lower the archival delay and retention floor

```bash
# fire the archival queue ~1 s after workflow close instead of up to 5 min
docker exec temporal-etcd etcdctl put /temporal/dynamicconfig/history.archivalProcessorArchiveDelay -- \
  '- value: 1s'

# allow namespace retention as short as 30 s
docker exec temporal-etcd etcdctl put /temporal/dynamicconfig/system.namespaceMinRetentionLocal -- \
  '- value: 30s'
```

### Step 2 — Set a short retention on the namespace

```bash
temporal operator namespace update -n default --retention 2m
```

With these three changes a workflow that closes should appear in MinIO within **~1–3 seconds**, and will drop out of primary persistence within **2 minutes** (making the archived copy the only copy).

### Step 3 — Verify

```bash
docker exec temporal-etcd etcdctl get /temporal/dynamicconfig/history.archivalProcessorArchiveDelay
docker exec temporal-etcd etcdctl get /temporal/dynamicconfig/system.namespaceMinRetentionLocal
temporal operator namespace describe -n default | grep Retention
```

### Step 4 — Restore normal settings when done

```bash
docker exec temporal-etcd etcdctl del /temporal/dynamicconfig/history.archivalProcessorArchiveDelay
docker exec temporal-etcd etcdctl del /temporal/dynamicconfig/system.namespaceMinRetentionLocal
temporal operator namespace update -n default --retention 72h
```

Deleting a key from etcd causes the server to fall back to its compiled-in default immediately.
