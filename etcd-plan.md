# Plan: etcd-backed dynamic config

Replace the file-polled dynamic config with a live etcd-backed client.
Changes propagate in near-realtime via etcd watch instead of the current 60s poll.

The extracted client lives at `~/devel/temporal-etcd-dynconfig`.

---

## Table of contents

- [Why a custom binary is required](#why-a-custom-binary-is-required)
- [Step 1 — Add etcd to compose-services.yml](#step-1--add-etcd-to-compose-servicesyml)
- [Step 2 — Custom server binary](#step-2--custom-server-binary)
- [Step 3 — Update compose-services.yml](#step-3--update-compose-servicesyml)
- [Step 4 — Update the config template](#step-4--update-the-config-template)
- [Step 5 — Seed etcd with current config values](#step-5--seed-etcd-with-current-config-values)
- [Step 6 — Build and validate](#step-6--build-and-validate)
- [Rollback](#rollback)
- [What is NOT changing](#what-is-not-changing)
- [Writing config values after setup](#writing-config-values-after-setup)
- [What happens when you push a bad value via etcdctl](#what-happens-when-you-push-a-bad-value-via-etcdctl)
  - [1. Invalid YAML](#1-invalid-yaml--broken-structure-missing-dash-bad-indentation)
  - [2. Valid YAML, wrong type](#2-valid-yaml-wrong-type-string-where-int-expected-etc)
  - [3. Typo in key name](#3-typo-in-key-name)
  - [Summary table](#summary-table)
  - [Planned improvement](#what-to-do-about-it--planned-improvement)
- [Managing config across separate clusters (prod / dev / loadtest)](#managing-config-across-separate-clusters-prod--dev--loadtest)
  - [Key layout](#key-layout)
  - [Cluster env var](#cluster-env-var)
  - [Writing config to a specific cluster](#writing-config-to-a-specific-cluster)
  - [Seeding a new cluster](#seeding-a-new-cluster)
  - [Applying the same change to all clusters at once](#applying-the-same-change-to-all-clusters-at-once)
  - [Viewing config for a specific cluster](#viewing-config-for-a-specific-cluster)
  - [Deleting a key](#deleting-a-key-revert-to-compiled-in-default)
  - [Shared etcd vs separate etcd per environment](#shared-etcd-vs-separate-etcd-per-environment)
  - [Typical config values per environment](#typical-config-values-per-environment)
- [How it works under the hood](#how-it-works-under-the-hood)
  - [What happens when you do etcdctl put](#what-happens-when-you-do-etcdctl-put)
  - [Multiple clusters, one etcd instance](#multiple-clusters-one-etcd-instance)
  - [What happens at cluster startup](#what-happens-at-cluster-startup)
  - [Watch supervisor: surviving etcd disruptions at runtime](#watch-supervisor-surviving-etcd-disruptions-at-runtime)

---

## Why a custom binary is required

The official `temporalio/server` image only supports file-based dynamic config
(`FileBasedClientConfig` in the YAML). The `temporal.WithDynamicConfigClient()`
server option — which is what lets you plug in a custom client — requires a
custom-built binary. Everything else (config loading, CLI flags, persistence,
auth) stays identical to the official binary.

---

## Step 1 — Add etcd to compose-services.yml

Add a single-node etcd service. No TLS, no auth for dev/test.

```yaml
etcd:
  image: gcr.io/etcd-development/etcd:v3.5.12
  container_name: temporal-etcd
  ports:
    - "2379:2379"
  command:
    - etcd
    - --advertise-client-urls=http://0.0.0.0:2379
    - --listen-client-urls=http://0.0.0.0:2379
  networks:
    - temporal-network
```

---

## Step 2 — Custom server binary

Create a `server/` directory with three files.

### server/main.go

Copy `cmd/server/main.go` from the OSS temporal repo exactly, with one addition
inside the `start` action — before calling `temporal.NewServer()`:

```go
// After cfg and logger are built, before temporal.NewServer():

var dcOpt temporal.ServerOption
if endpoints := os.Getenv("ETCD_ENDPOINTS"); endpoints != "" {
    etcdCfg := etcddynconfig.Config{
        EtcdConfigs: []etcddynconfig.EtcdConfig{{
            Name:      "primary",
            Endpoints: strings.Split(endpoints, ","),
        }},
        GlobalKeyPrefix: getEnvOrDefault("ETCD_KEY_PREFIX", "temporal/dynamicconfig/"),
        DisableTLS:      os.Getenv("ETCD_DISABLE_TLS") != "false",
        ClientName:      getEnvOrDefault("ETCD_CLIENT_NAME", "temporal-server"),
    }
    etcdCfg.EnsureDefaults()

    etcdClient := etcddynconfig.NewEtcdClient(etcdCfg, logger)
    dcClient, err := etcddynconfig.NewClient(ctx, etcdClient, etcdCfg.GlobalKeyPrefix, logger)
    if err != nil {
        return cli.Exit(fmt.Sprintf("Unable to create etcd dynamic config client: %v", err), 1)
    }
    defer dcClient.Stop()
    defer etcdClient.Close()
    dcOpt = temporal.WithDynamicConfigClient(dcClient)
    logger.Info("Using etcd-backed dynamic config", tag.NewStringTag("endpoints", endpoints))
}
```

Then pass `dcOpt` into `temporal.NewServer()` — if nil it is a no-op so file-based
config continues to work when `ETCD_ENDPOINTS` is not set.

The helper:

```go
func getEnvOrDefault(key, def string) string {
    if v := os.Getenv(key); v != "" {
        return v
    }
    return def
}
```

Imports to add on top of the official ones:

```go
etcddynconfig "github.com/temporalio/temporal-etcd-dynconfig"
```

### server/go.mod

```
module github.com/temporalio/my-temporal-dockercompose/server

go 1.26.2

require (
    github.com/temporalio/temporal-etcd-dynconfig v0.0.0-00010101000000-000000000000
    github.com/urfave/cli/v2 v2.x.x
    go.temporal.io/server v0.0.0-00010101000000-000000000000
    _ go.temporal.io/server/common/persistence/sql/sqlplugin/postgresql
    _ go.temporal.io/server/common/persistence/sql/sqlplugin/mysql
    _ go.temporal.io/server/common/persistence/sql/sqlplugin/sqlite
)

replace (
    go.temporal.io/server                          => /Users/tsurdilo/devel/temporal/temporal
    github.com/temporalio/temporal-etcd-dynconfig  => /Users/tsurdilo/devel/temporal-etcd-dynconfig
)
```

Run `go mod tidy` after creating this file.

### Dockerfile

Multi-stage build. Stage 1 compiles the binary; stage 2 produces the final image.

```dockerfile
FROM golang:1.26-alpine AS builder
WORKDIR /build
COPY server/ .
RUN go mod download
RUN CGO_ENABLED=0 GOOS=linux go build -o temporal-server .

FROM alpine:3.19
RUN apk add --no-cache ca-certificates tzdata
COPY --from=builder /build/temporal-server /usr/local/bin/temporal-server
ENTRYPOINT ["temporal-server"]
CMD ["start", "--allow-no-auth"]
```

Note: the official image entrypoint is the same `temporal-server start` invocation.
The config template and volume mounts continue to work unchanged.

---

## Step 3 — Update compose-services.yml

For all eight Temporal services
(`temporal-history`, `temporal-history2`, `temporal-matching`, `temporal-matching2`,
`temporal-frontend`, `temporal-frontend2`, `temporal-internal-frontend`, `temporal-worker`):

**1. Swap the image:**

```yaml
# before
image: temporalio/server:${TEMPORAL_SERVER_IMG}

# after
image: temporal-custom:${TEMPORAL_SERVER_IMG}
```

**2. Add environment variables:**

```yaml
environment:
  # existing vars unchanged ...
  ETCD_ENDPOINTS: "etcd:2379"
  ETCD_KEY_PREFIX: "temporal/dynamicconfig/"
  ETCD_CLIENT_NAME: "temporal-server"
  ETCD_DISABLE_TLS: "true"
```

**3. Add dependency:**

```yaml
depends_on:
  etcd:
    condition: service_started
  # existing depends_on entries unchanged ...
```

---

## Step 4 — Update the config template

In `template/my_config_template.yaml`, remove the `dynamicConfigClient` block
(currently lines 436–438):

```yaml
# DELETE these lines once the custom image is live:
dynamicConfigClient:
  filepath: "{{ default .Env.DYNAMIC_CONFIG_FILE_PATH "/etc/temporal/config/dynamicconfig/docker.yaml" }}"
  pollInterval: "60s"
```

Keep them commented out until you validate the new setup, so rollback is one
line change.

---

## Step 5 — Seed etcd with current config values

Run this once on first start to migrate `dynamicconfig/development.yaml` into etcd.
Create `script/seed-etcd.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail

ETCD_ENDPOINT="${ETCD_ENDPOINT:-127.0.0.1:2379}"
KEY_PREFIX="${KEY_PREFIX:-temporal/dynamicconfig/}"
CONFIG_FILE="${CONFIG_FILE:-dynamicconfig/development.yaml}"

echo "Seeding etcd from $CONFIG_FILE ..."

# Requires yq (https://github.com/mikefarah/yq) and etcdctl
while IFS= read -r key; do
    value=$(yq e ".$key" "$CONFIG_FILE")
    etcd_key="${KEY_PREFIX}${key}"
    echo "  PUT $etcd_key"
    etcdctl --endpoints="$ETCD_ENDPOINT" put "$etcd_key" "$value"
done < <(yq e 'keys | .[]' "$CONFIG_FILE")

echo "Done."
```

Or use `etcdctl` directly for individual keys — the value format is a YAML list:

```bash
etcdctl put temporal/dynamicconfig/frontend.rps '
- value: 1200
  constraints: {}
'
```

---

## Step 6 — Build and validate

```bash
# 1. Start etcd only
docker compose up -d etcd

# 2. Seed current config values
ETCD_ENDPOINT=127.0.0.1:2379 ./script/seed-etcd.sh

# 3. Verify values are in etcd
etcdctl --endpoints=127.0.0.1:2379 get temporal/dynamicconfig/ --prefix

# 4. Build custom image
docker build -t temporal-custom:$(grep TEMPORAL_SERVER_IMG .env | cut -d= -f2) .

# 5. Start the full stack
docker compose up -d

# 6. Verify live config change propagates
etcdctl --endpoints=127.0.0.1:2379 put temporal/dynamicconfig/frontend.rps '
- value: 999
  constraints: {}
'
# Watch the temporal-frontend logs for:
# "dynamic config changed: frontend.rps old=[...] new=[{constraints:{} value:999}]"
```

---

## Rollback

To revert to the official image and file-based config:

1. In `compose-services.yml`, change `temporal-custom` back to `temporalio/server`
2. Uncomment the `dynamicConfigClient` block in `template/my_config_template.yaml`
3. `docker compose up -d`

etcd data is harmless to leave in place.

---

## What is NOT changing

| Component | Status |
|---|---|
| `dynamicconfig/development.yaml` | Kept as reference; values seeded to etcd once |
| `compose-postgres.yml` | Untouched |
| `poller/` health poller | Untouched |
| `script/setup.sh` | Untouched |
| Grafana / Prometheus / Loki stack | Untouched |
| Replication compose file | Separate work — would need its own key prefix (e.g. `c1/dynamicconfig/`, `c2/dynamicconfig/`) |

---

## Writing config values after setup

```bash
# Global override
etcdctl put temporal/dynamicconfig/frontend.rps '
- value: 1200
  constraints: {}
'

# Namespace-scoped override with global fallback
etcdctl put temporal/dynamicconfig/frontend.rps '
- value: 500
  constraints:
    namespace: my-namespace
- value: 1200
  constraints: {}
'

# Delete a key (server reverts to compiled-in default)
etcdctl del temporal/dynamicconfig/frontend.rps

# List all current overrides
etcdctl get temporal/dynamicconfig/ --prefix
```

Changes take effect within seconds on all server instances simultaneously.

---

## What happens when you push a bad value via etcdctl

etcd accepts any string — it has no schema. Validation happens on the Temporal
server side, in two separate layers, and the failure modes are not obvious.

### Three ways a bad write can go wrong

#### 1. Invalid YAML — broken structure, missing dash, bad indentation

```bash
# missing leading dash — not a list
etcdctl put temporal/dynamicconfig/frontend.rps '
  value: 1200
  constraints: {}
'
```

`unmarshalValues()` in the etcd client fails to parse the YAML. Inside
`applyEvents()` the error is caught and the key is skipped:

```go
cvs, err := c.unmarshalValues(ev.Kv.Value)
if err != nil {
    c.logger.Error("failed to parse dynamic config value", ...)
    continue  // old value for this key is left untouched
}
```

**Result:** ERROR log with the raw value. The existing in-memory value for that
key is preserved — the server keeps running with the last good value. No crash.

---

#### 2. Valid YAML, wrong type (string where int expected, etc.)

```bash
# "lots" is a string, frontend.rps expects an int
etcdctl put temporal/dynamicconfig/frontend.rps '
- value: "lots"
  constraints: {}
'
```

This parses fine as YAML and lands in the in-memory map. The client sees no
error. The failure hits later, when Temporal code reads the key via
`dynamicconfig.Collection`. The collection tries `convertInt("lots")`, fails,
and silently falls back:

```go
// collection.go
if err != nil {
    if c.throttleLog() {
        c.logger.Warn("Failed to convert value, using default", ...)
    }
    return def, usingDefaultValue  // compiled-in default used instead
}
```

**Result:** A throttled WARN in the server logs. The server silently uses the
compiled-in default for that key. The write appeared to succeed, the in-memory
map was updated, but the value is never actually used. **This is the most
dangerous failure mode** — easy to miss, no signal at the write site.

---

#### 3. Typo in key name

```bash
# rsp instead of rps — nobody ever reads this key
etcdctl put temporal/dynamicconfig/frontend.rsp '
- value: 1200
  constraints: {}
'
```

Parses fine. Lands in the map. No code ever looks up `frontend.rsp` so it sits
unused in etcd forever. The intended key is unaffected.

**Result:** Completely silent. No log, no error, no effect.

---

### Summary table

| Mistake | Detected where | Log level | Effect on server |
|---|---|---|---|
| Invalid YAML | `unmarshalValues()` in etcd client | ERROR | Key skipped, old value kept |
| Valid YAML, wrong Go type | `convertXxx()` in OSS Collection | WARN (throttled) | Silently falls back to compiled-in default |
| Typo in key name | Never | Nothing | Value silently ignored forever |

The server **never crashes** from a bad config write — it always has a fallback.
But two of the three failure modes produce no signal at the write site and only
a throttled warn buried in server logs.

---

### What to do about it — planned improvement

The `temporal-etcd-dynconfig` module (`~/devel/temporal-etcd-dynconfig`) needs
a `Validate(key, rawYAML)` method that runs the same parse + type conversion
pipeline before writing to etcd, rejecting bad input at the source rather than
discovering it silently at read time on the server.

Until that is built, the safe workflow for manual `etcdctl` writes is:

1. **Check the key name first** — look it up in `dynamicconfig/development.yaml`
   or run `etcdctl get temporal/dynamicconfig/ --prefix --keys-only` to see what
   keys are currently loaded. Any key not on that list is either unknown or a
   typo.

2. **Verify the type** — check the OSS source at
   `~/devel/temporal/temporal/common/dynamicconfig/constants.go` for the key
   definition. The type is in the `NewGlobalXxxSetting` / `NewNamespaceXxxSetting`
   call — `Int`, `Bool`, `Float`, `Duration`, `String`, or `Map`.

3. **After writing, confirm it loaded** — watch the server logs for the
   `dynamic config changed` INFO line (logged by `logDiff` in the etcd client).
   If you see it, the YAML parsed correctly. If you do not see it within a
   second or two, the write was silently rejected — check for the ERROR log.

4. **After confirming load, check for type errors** — search the server logs for
   `Failed to convert value, using default` for that key. If that line appears,
   the value loaded but was the wrong type and the server ignored it.

---

## Managing config across separate clusters (prod / dev / loadtest)

Each cluster connects to etcd using its own `ETCD_KEY_PREFIX`. That prefix is
the only thing that determines which keys a cluster reads — clusters are fully
isolated from each other through it.

### Key layout

```
etcd
├── prod/dynamicconfig/frontend.rps
├── prod/dynamicconfig/history.cacheMaxSize
├── prod/dynamicconfig/matching.numTaskqueueReadPartitions
├── dev/dynamicconfig/frontend.rps
├── dev/dynamicconfig/history.cacheMaxSize
└── loadtest/dynamicconfig/frontend.rps
```

### Cluster env var

Set `ETCD_KEY_PREFIX` per cluster in the compose file or deployment config:

```yaml
# prod services
ETCD_KEY_PREFIX: "prod/dynamicconfig/"

# dev services
ETCD_KEY_PREFIX: "dev/dynamicconfig/"

# loadtest services
ETCD_KEY_PREFIX: "loadtest/dynamicconfig/"
```

Each cluster only ever reads and watches keys under its own prefix. A write to
`prod/dynamicconfig/` has zero effect on dev or loadtest, and vice versa.

### Writing config to a specific cluster

```bash
# update prod only
etcdctl put prod/dynamicconfig/frontend.rps '
- value: 5000
  constraints: {}
'

# update dev only
etcdctl put dev/dynamicconfig/frontend.rps '
- value: 200
  constraints: {}
'

# update loadtest only — e.g. crank up partitions for load testing
etcdctl put loadtest/dynamicconfig/matching.numTaskqueueReadPartitions '
- value: 16
  constraints: {}
'
```

### Seeding a new cluster

When you bring up a new cluster (e.g. a new loadtest environment), seed it from
your baseline config file or from an existing cluster's keys:

```bash
# seed loadtest from the same baseline as dev
ETCD_ENDPOINT=127.0.0.1:2379 KEY_PREFIX=loadtest/dynamicconfig/ ./script/seed-etcd.sh

# or copy all keys from dev to loadtest
etcdctl --endpoints=127.0.0.1:2379 get dev/dynamicconfig/ --prefix --print-value-only |
  paste - - |   # pair keys and values
  while read key value; do
    newkey="${key/dev\//loadtest/}"
    etcdctl put "$newkey" "$value"
  done
```

### Applying the same change to all clusters at once

Use a loop when you want a change to land everywhere simultaneously:

```bash
for cluster in prod dev loadtest; do
  etcdctl put ${cluster}/dynamicconfig/history.cacheMaxSize '
- value: 1024
  constraints: {}
'
done
```

### Viewing config for a specific cluster

```bash
# list all keys for prod
etcdctl get prod/dynamicconfig/ --prefix --keys-only

# list all keys and values for dev
etcdctl get dev/dynamicconfig/ --prefix

# compare a single key across all clusters
for cluster in prod dev loadtest; do
  echo "=== $cluster ==="
  etcdctl get ${cluster}/dynamicconfig/frontend.rps
done
```

### Deleting a key (revert to compiled-in default)

```bash
# revert frontend.rps to default on dev only
etcdctl del dev/dynamicconfig/frontend.rps

# revert the same key on all clusters
for cluster in prod dev loadtest; do
  etcdctl del ${cluster}/dynamicconfig/frontend.rps
done
```

### Shared etcd vs separate etcd per environment

| Setup | When to use |
|---|---|
| One etcd, separate prefixes | Local dev, test environments, anything non-production |
| Separate etcd cluster for prod | Recommended for production — a bad write can only affect the cluster it targets; no risk of a dev typo touching prod keys |

For production, point `ETCD_ENDPOINTS` at a dedicated etcd cluster that dev
operators do not have write access to. The prefix isolation approach is
convenient but relies on discipline; separate clusters give hard isolation.

### Typical config values per environment

As a reference, these are the keys most commonly tuned differently per environment:

| Key | prod | dev | loadtest |
|---|---|---|---|
| `frontend.rps` | high (e.g. 5000) | low (e.g. 200) | very high (e.g. 50000) |
| `frontend.namespaceRPS` | high | low | very high |
| `matching.numTaskqueueReadPartitions` | moderate (4–8) | 1 | high (16–32) |
| `matching.numTaskqueueWritePartitions` | moderate (4–8) | 1 | high (16–32) |
| `history.cacheMaxSize` | large | small | large |
| `history.hostLevelCacheMaxSize` | large | small | large |
| `worker.schedulerNamespaceStartWorkflowRPS` | moderate | low | high |

---

## How it works under the hood

### What happens when you do `etcdctl put`

When the Temporal server starts, `NewClient()` does two things in order:

**1. Bulk load** — calls `etcd.Get(prefix, WithPrefix())` and reads every key
under `temporal/dynamicconfig/` into an in-memory map. The server now has all
current values available before it starts serving traffic.

**2. Opens a watch stream** — calls `etcd.Watch(prefix, WithPrefix(), WithRev(R))`
where `R` is the etcd revision returned by the bulk load. This tells etcd:
*"send me every change to any key under this prefix, starting from exactly where
I left off."*

From that point on, etcd pushes change events to the client over a persistent
gRPC stream. When you run `etcdctl put temporal/dynamicconfig/frontend.rps ...`,
etcd immediately pushes a `PUT` event to every connected watcher. The client's
`applyEvents()` function:

1. Parses the YAML value into `[]ConstrainedValue`
2. Clones the in-memory map and updates the key
3. Atomically swaps the new map in
4. Calls every registered `Subscribe` callback with the changed keys

That callback is what Temporal's `dynamicconfig.Collection` registered at startup
— it invalidates its own cache for those keys. The next time any Temporal code
reads `frontend.rps` it gets the new value straight from the updated map.

The full path from your `etcdctl put` to the server using the new value:

```
etcdctl put
  → etcd server stores the new value
  → pushes PUT event over gRPC stream to all watchers
  → client.watchOnce() receives the event
  → applyEvents() updates in-memory map, notifies subscribers
  → dynamicconfig.Collection invalidates its cache
  → next read by Temporal code returns the new value
```

Wall-clock time: **under a second** in practice. No polling, no file read,
no restart.

---

### Multiple clusters, one etcd instance

Each Temporal server process opens its own independent watch stream to etcd.
They all watch the same prefix. One `etcdctl put` fans the event out to every
connected watcher simultaneously:

```
etcdctl put frontend.rps = 1200
        │
        ▼
  ┌─────────────────────┐
  │        etcd          │
  │  temporal/dynconfig/ │
  │  frontend.rps = 1200 │
  └──────────┬──────────┘
             │  pushes event to all watchers
    ┌─────────┼─────────┐
    ▼         ▼         ▼
frontend1  frontend2  history1   ... all 8 services update simultaneously
```

For the replication setup (`compose-services-replication.yml`) with clusters
c1 and c2, they can share the same etcd instance using different key prefixes:

```
c1 services → ETCD_KEY_PREFIX=c1/dynamicconfig/
c2 services → ETCD_KEY_PREFIX=c2/dynamicconfig/
```

`etcdctl put c1/dynamicconfig/frontend.rps` updates only c1.
`etcdctl put c2/dynamicconfig/frontend.rps` updates only c2.
Or point both at the same prefix to share config across clusters.

---

### What happens at cluster startup

`NewClient()` calls `loadAll()` before the watch stream opens. This is the
authoritative initial read — the server starts with whatever is currently in
etcd, not with any file.

```
1. loadAll()     GET temporal/dynamicconfig/* from etcd
                 populate in-memory map with all current values
                 capture etcd revision R

2. Watch(rev=R)  open stream starting from revision R
                 any write that happened between step 1 and step 2
                 has revision > R and is replayed — no gap possible

3. Server starts dynamicconfig.Collection reads from in-memory map
                 all values already present
```

**If etcd is empty** (nothing seeded yet), `loadAll()` returns an empty map
and the server uses compiled-in defaults for every key — same behaviour as an
empty `development.yaml`. This is why running `seed-etcd.sh` before first
startup matters.

**If etcd is unreachable at startup**, `NewEtcdClient()` retries 3 times with
exponential backoff (2s initial, 2× coefficient) then calls `logger.Fatal` —
the server refuses to start rather than silently falling back to defaults.

---

### Watch supervisor: surviving etcd disruptions at runtime

The watch stream can be interrupted by etcd leader elections, connection resets,
or the server compacting its revision history. The supervisor loop handles all
of these without any operator action:

| Event | Behaviour |
|---|---|
| Transient stream error | Reload all values from etcd, reopen watch from new revision |
| etcd compacts past last-seen revision | Same — reload and resubscribe |
| Leader election / connection reset | Same |
| `dcClient.Stop()` called | Exits cleanly, no reload |

Backoff on reload failure: 100ms → doubles each attempt → caps at 30s.

---

### Summary table

| Question | Answer |
|---|---|
| How does `etcdctl put` update the server? | etcd pushes a watch event → in-memory map updated in under a second |
| Is a restart needed? | No — the map swap is atomic and the Collection cache is invalidated immediately |
| Multiple clusters, one etcd? | Yes — each server process has its own watch stream; one put fans out to all |
| Cluster isolation with shared etcd? | Use a different `ETCD_KEY_PREFIX` per cluster |
| What does the server read at startup? | `loadAll()` bulk-reads etcd; watch opens from the same revision so no gap |
| What if etcd is empty at startup? | Compiled-in defaults are used — seed etcd first if you want your overrides |
| What if etcd is unreachable at startup? | Server refuses to start (Fatal) |
| What if etcd goes down after startup? | In-memory map keeps last known values; supervisor reconnects automatically |
