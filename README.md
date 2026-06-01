## Table of Contents

- [About](#about)
- [Deploying your Temporal service on Docker](#deploying-your-service)
- [Some usueful Docker commands](#some-useful-docker-commands)
- [Troubleshoot](#troubleshoot)
- [Extra](#extra)
  - [Dual Visibility](#dual-visibility)
  - [Multi Cluster Replication setup](#multi-cluster-replication-setup)
  - [Version Upgrades](#version-upgrades)
  - [Plaintext Payload Interceptor](#plaintext-payload-interceptor)
  - [MinIO Archival](#minio-archival)
  
## About

This repo includes some experiments on self-deploying Temporal server via Docker 
Compose and Swarm.

It can serve as reference to community for a number of Docker related 
deployment questions.
For this repo we use PostgreSQL for persistence for temporal db. We set up 
advanced visibility with Postgres DB (but options with OpenSearch /  ElasticSearch are possible) for temporal_visibility.
It also shows how to set up internal frontend service and use by worker service (even tho we do not set up yet
custom authorizer/claims mapper).
It also shows how to set up server grpc tracing with otel collector and can be visualized with Jaeger. 

In addition it has a out of box sample of setting up multi cluster replication and some cli commands 
to get it up and running locally.

### Dynamic config

This setup uses [temporal-etcd-dynconfig](https://github.com/tsurdilo/temporal-etcd-dynconfig) instead of the default file-based dynamic config client. Dynamic config values are stored in etcd and propagate to all server hosts simultaneously via etcd watch — no polling, no per-host file management.

etcd runs as a container in the compose stack. An initial set of default values is seeded from `defaults.yaml` on first start. You can inspect and update values at any time using [etcdkeeper](http://localhost:8086/etcdkeeper/) or `etcdctl`:

```bash
# Update a value — all 8 server containers pick it up within milliseconds
docker exec temporal-etcd etcdctl --endpoints=http://localhost:2379 \
  put /temporal/dynamicconfig/frontend.globalNamespaceRPS -- "- value: 2000"

# List all current dynamic config values
docker exec temporal-etcd etcdctl --endpoints=http://localhost:2379 \
  get /temporal/dynamicconfig/ --prefix --keys-only
```

See the [temporal-etcd-dynconfig](https://github.com/tsurdilo/temporal-etcd-dynconfig) repo for full documentation on key format, constraints, metrics, and multi-cluster setup.

## Deploying your service

This setup targets more of production environments as it deploys each Temporal server role
(frontend, matching, history, worker) in individual containers. 
The setup runs two instances (containers) for frontend, matching and history hosts.
Service sets up 2K shards, so ~1K per history host.

Each server role container exposes server metrics under its own port.
Note that bashing into the admin-tools image also gives you access to tctl as well as the temporal-tools for different
dbs. 

### How to start

First we need to install the loki plugin (you have to do this just one time)

    docker plugin install grafana/loki-docker-driver:latest --alias loki --grant-all-permissions

Check if the plugin is installed:

    docker plugin ls

(should see the Loki Logging Driver plugin installed

Repo contains a "poller" docker image that needs to be built the first time. This image
calls DeepHealthCheck frontend api periodically which allows us to monitor our 
history services, and will allow us to emit the _host_health_ metric. To build it every time
start with "--build": 

     docker network create temporal-network
     docker compose -f compose-postgres.yml -f compose-services.yml up --build --detach


If you have already built the image and you didnt change the go code in /poller dir, then just run:

    docker network create temporal-network
    docker compose -f compose-postgres.yml -f compose-services.yml up --detach

## Check if it works

### If you are running tctl locally

    tctl cl h

should return "SERVING"

### If you don't have tctl locally

Bash into admin-tools container and run tctl (you can do this from your machine if you have tctl installed too)

    docker container ls --format "table {{.ID}}\t{{.Image}}\t{{.Names}}"

copy the id of the temporal-admin-tools container

    docker exec -it <admin tools container id> bash 
    tctl cl h

you should see response:

    temporal.api.workflowservice.v1.WorkflowService: SERVING

Note: if you set up HAProxy instead of default Envoy, and dont see "SERVING" but rather "context deadline exceeded errors"
restart the "temporal-haproxy" container. Not yet sure why but haproxy is 
doing something weird. Once you restart it admin-tools container should be able to finish
its setup-server script and things should work fine. If anyone can figure out the issue 
please commit PR. Thanks.

We start postgres from a separate compose file but you don't have to and can combine them if you want.

By the way, if you want to docker exec into the postgres container do:

    docker exec -it <temporal-postgres container id> psql -U temporal
    \l

which should show the temporal and temporal_visiblity dbs

(You can do this via Portainer as well, this just shows the "long way")

In addition let's check out the rings in cluster:

    tctl adm cl d

You should see two members for frontend, matching and history service rings. One 
for the worker service (typically you dont need to scale worker service)

### Health check service containers

* Frontend (via grpcurl)
```
grpcurl -plaintext -d '{"service": "temporal.api.workflowservice.v1.WorkflowService"}' 127.0.0.1:7233 grpc.health.v1.Health/Check
```

again you can just run `tctl cl h` too

* Frontend (via grpc-health-probe)

```
grpc-health-probe -addr=localhost:7233 -service=temporal.api.workflowservice.v1.WorkflowService
```

To check the two frontend services individually:

```
grpc-health-probe -addr=localhost:7236 -service=temporal.api.workflowservice.v1.WorkflowService
grpc-health-probe -addr=localhost:7237 -service=temporal.api.workflowservice.v1.WorkflowService
```

* Internal Frontend (via grpc-health-probe)
```
grpc-health-probe -addr=localhost:7233 -service=temporal.api.workflowservice.v1.WorkflowService
```

* Matching (via grpc-health-probe)

```
grpc-health-probe -addr=localhost:7235 -service=temporal.api.workflowservice.v1.MatchingService
grpc-health-probe -addr=localhost:7239 -service=temporal.api.workflowservice.v1.MatchingService
```

* History via grpc-health-probe)

```
grpc-health-probe -addr=localhost:7234 -service=temporal.api.workflowservice.v1.HistoryService
grpc-health-probe -addr=localhost:7238 -service=temporal.api.workflowservice.v1.HistoryService
```

### Http API
Added support for HTTP api which became available since 1.22.0 server release
to test you can do for example:

```
curl http://localhost:7243/api/v1/namespaces/default
```

once your service is up and running. For more info see [here](https://github.com/temporalio/api/blob/master/temporal/api/workflowservice/v1/service.proto)

### Parsing static config since server release 1.30
Since server release 1.30 we need to fetch the embedded template and use that to display static config.
It's no longer just created by dockerize in /etc/temporal/config/docker.yaml
Can do something like this (change url to embedded config to reflect your server version so you get right 
embedded static config template)

     wget -q -O /tmp/development.yaml \
       https://raw.githubusercontent.com/temporalio/temporal/v1.31.0/common/config/config_template_embedded.yaml

     temporal-server --config /tmp render-config > /tmp/resolved.yaml && cat /tmp/resolved.yaml

### Links

* Server metrics (raw)
  * [History Service1](http://localhost:8000/metrics)
  * [History Service2](http://localhost:8005/metrics)
  * [Matching Service1](http://localhost:8001/metrics)
  * [Matching Service2](http://localhost:8006/metrics)
  * [Frontend Service1](http://localhost:8002/metrics)
  * [Frontend Service2](http://localhost:8004/metrics)
  * [Internal Frontend](http://localhost:8007/metrics)
  * [Worker Service](http://localhost:8003/metrics)
* [Prometheus targets (scrape points)](http://localhost:9090/targets)
* [Grafana (includes server, sdk, docker, and postgres dashboards)](http://localhost:8085/)
  * No login required
  * In order to scrape docker system metrics add "metrics-addr":"127.0.0.1:9323" to your docker daemon.js, on Mac this is located at ~/.docker/daemon.json
  * Go to "Explore" and select Loki data source to run LogQL against server metrics
* [Web UI v2](http://localhost:8080/namespaces/default/workflows)
* [Web UI v1](http://localhost:8088/)
* [Portainer](http://localhost:9000/)
  * Note you will have to create an user the first time you log in
  * Yes it forces a longer password but whatever
* [Jaeger](http://localhost:16686/) - includes server grpc traces
* [PgAdmin](http://localhost:5050/) (username: pgadmin4@pgadmin.org passwd: admin)
* [etcd keeper](http://localhost:8086/etcdkeeper/)
* [minio console](http://localhost:9011/login) (username: minioadmin passwd: minioadmin)
* [cAdvisor](http://localhost:9092/docker) to monitor docker containers 


### Custom docker template

Docker server image by default use [this](https://github.com/temporalio/temporal/blob/master/docker/config_template.yaml) server config template.
This is a base template that may not fit everyones needs. You an define your custom configuration template if you wish
and this is what we are doing via [my_config_template.yaml](template/my_config_template.yaml).
With this you can customize the template as you wish, for example you could configure env vars for namespace setup, like set up 
s3 archival etc which is not possible with the default template.

### Exclude metrics 

This demo also shows how to exclude certain metrics produced by Temporal services 
when scraping their metric endpoints, for example [here](/deployment/prometheus/config.yml) we drop all metrics 
for the "temporal_system" namespace. 

### Client access
Envoy / HAProxy / NGINX role is exposed on 127.0.0.1:7233 (so all SDK samples should work w/o changes). It is load balancing the two
Temporal frontend services defined in the docker compose.

### Envoy
This sample uses Envoy load balancing by default. Check out the Envoy config file [here](/deployment/envoy/envoy.yaml) and make
any necessary changes. Note this is just a demo so you might want to
update the values where needed for your prod env.
Envoy pushes access logs to stdout and is picked up by loki, so can run
all queries in Grafana. This includes grpc code and size and everything.

### HAProxy
You can set up HAProxy load balancing if you want. It load balances
our two frontend services. Check out the HAProxy config file [here](/deployment/haproxy/haproxy.cfg) and make
any necessary changes. Note this is just a demo so you might want to 
update the values where needed for your prod env.

I have ran into some issues with this HAProxy setup, specifically
sometimes having to restart its container in order for admin-tools to be able to complete
setup, as well as for executions first workflow task timing out.
Pretty sure this has to do with some problem with the config, maybe someone could 
look and fix. 

### NGINX
You can also have NGINX configured and use it for load balancing. It load balanced our two temporal frontends.
Check out the NGINX config file [here](/deployment/nginx/nginx.conf) and make any necessary adjustments. This is just a demo remember and 
for production use you should make sure to update values where necessary.

## Some useful Docker commands
    docker-compose down --volumes
    docker system prune -a
    docker volume prune

    docker compose up --detach
    docker compose up --force-recreate

    docker stop $(docker ps -a -q)
    docker rm $(docker ps -a -q)

    docker compose -f compose-postgres.yml -f compose-services.yml down --remove-orphans
    docker network rm temporal-network

    # remove etcd (dynamic config) volume
    docker volume rm my-temporal-dockercompose_etcd-data

    # restart + full rebuild of custom server image
    docker compose -f compose-postgres.yml -f compose-services.yml down
    docker compose -f compose-postgres.yml -f compose-services.yml build \
      temporal-history temporal-history2 \
      temporal-matching temporal-matching2 \
      temporal-frontend temporal-frontend2 \
      temporal-internal-frontend \
      temporal-worker
    docker compose -f compose-postgres.yml -f compose-services.yml up --detach


## Troubleshoot

* "Not enough hosts to serve the request"
  * Can happen on startup when some temporal service container did not start up properly, run the docker compose command again typically fixes this

## Extra
Here are some extra configurations, try them out and please report any errors.

## Dual Visibility

Dual visibility allows you to configure a secondary visibility store. 
One use case of having dual visibility is if you need to migrate from one store to another or switch to secondary store
in case of failures on primary one. This is typically what you want to do in a production env.

In this sample we set up dual visibility for our SQL visibility setup (Postgres).
Please note that we set this up on the same db instance. This demo uses a single postgres to set up 
all dbs, primary, visibility, and secondary visibility. For a prod env you might want to separate these
to 3 completely separate envs (recommended). 

The key dynamic config setting for dual visibility is to enable writes to both primary and secondary vis:

      system.secondaryVisibilityWritingMode:
        - value: "dual"
          constraints: {}
      system.enableReadFromSecondaryVisibility:
        - value: false
          constraints: {}

This sets up Temporal to write visibility data to both primary and secondary vis stores. 
Let's say you run into issues on this primary store, via dynamic config again you can switch read to secondary

      system.enableReadFromSecondaryVisibility:
        - value: true
          constraints: {}

If you experience complete outage of primary vis store, you can change your static config as needed and then
again look at your dynamic config to write to primary and-or secondary as again needed.

## Multi Cluster Replication Setup

Covers: start two clusters → connect them → promote a namespace to global → backfill existing executions → failover to c2 → decommission c1.

---

### Phase 1 — Start the clusters

**1. Clear your Docker environment** (see [Some useful Docker commands](#some-useful-docker-commands))

**2. Create the network and start both clusters**

```bash
docker network create temporal-network-replication
docker compose -f compose-services-replication.yml up --detach
```

**3. Verify both clusters are healthy**

```bash
temporal --address 127.0.0.1:7233 operator cluster health
temporal --address 127.0.0.1:2233 operator cluster health
```

**4. Confirm cluster names**

```bash
temporal --address 127.0.0.1:7233 operator cluster describe -o json | jq .clusterName
# expected: "c1"

temporal --address 127.0.0.1:2233 operator cluster describe -o json | jq .clusterName
# expected: "c2"
```

**5. Get container IPs** — needed for the upsert commands in Phase 3

```bash
docker inspect -f '{{range.NetworkSettings.Networks}}{{.IPAddress}}{{end}}' temporalc1
docker inspect -f '{{range.NetworkSettings.Networks}}{{.IPAddress}}{{end}}' temporalc2
```

Note these as `TEMPORALC1_IP` and `TEMPORALC2_IP`.

---

### Phase 2 — Seed workflows on c1

**6. Create the test namespace on c1**

```bash
temporal --address 127.0.0.1:7233 operator namespace create replicationtest
```

**7. Start sample workflows**

Run a mix of short (completing) and long-running workflows so you can see both completed and running executions replicate. This Java sample creates 30 executions — 20 complete, 10 remain running:

https://gist.github.com/tsurdilo/f0ef3ea2940e877aaec7489370ae099c

**8. Verify workflows are on c1**

http://localhost:8081/namespaces/replicationtest/workflows

**9. Confirm namespace does not exist on c2 yet**

http://localhost:8082/namespaces/replicationtest/workflows

---

### Phase 3 — Connect the clusters

Each cluster stores its own peer registry locally — both directions must be run independently.

**10. Register c2 as a peer on c1**

```bash
temporal --address 127.0.0.1:7233 operator cluster upsert \
  --enable-connection \
  --enable-replication \
  --frontend-address "TEMPORALC2_IP:2233"
```

**11. Register c1 as a peer on c2**

```bash
temporal --address 127.0.0.1:2233 operator cluster upsert \
  --enable-connection \
  --enable-replication \
  --frontend-address "TEMPORALC1_IP:7233"
```

**12. Verify both clusters see each other**

```bash
temporal --address 127.0.0.1:7233 operator cluster list
temporal --address 127.0.0.1:2233 operator cluster list
```

---

### Phase 4 — Promote namespace to global and enable replication

**13. Promote `replicationtest` from local to global namespace**

```bash
temporal --address 127.0.0.1:7233 operator namespace update \
  --namespace replicationtest \
  --promote-global
```

**14. Verify it is now a global namespace**

```bash
temporal --address 127.0.0.1:7233 operator namespace describe \
  --namespace replicationtest -o json | jq .isGlobalNamespace
# expected: true
```

**15. Add both clusters to the namespace replication config**

```bash
temporal --address 127.0.0.1:7233 operator namespace update \
  --namespace replicationtest \
  --cluster c1 \
  --cluster c2
```

**16. Verify replication config shows both clusters**

```bash
temporal --address 127.0.0.1:7233 operator namespace describe \
  --namespace replicationtest -o json | jq .replicationConfig
```

**17. Confirm namespace now exists on c2**

http://localhost:8082/namespaces/replicationtest/workflows

No workflows yet — the namespace was replicated but existing executions are not backfilled automatically.

---

### Phase 5 — Backfill existing executions

New executions started after step 15 replicate automatically. The 30 existing executions on c1 need to be force-replicated.

**18. Start the force-replication system workflow on c1**

```bash
temporal --address 127.0.0.1:7233 workflow start \
  --namespace temporal-system \
  --type force-replication \
  --task-queue default-worker-tq \
  --input '{"Namespace": "replicationtest", "ConcurrentActivityCount": 4, "OverallRps": 80}'
```

**19. Monitor until complete**

http://localhost:8081/namespaces/temporal-system/workflows?query=WorkflowType%3D%22force-replication%22

**20. Verify all executions are on c2**

http://localhost:8082/namespaces/replicationtest/workflows

Expected: 20 completed and 10 running executions.

---

### Phase 6 — Failover to c2

`namespace-handover` is a safe failover — it waits for replication lag to drain before flipping the active cluster, unlike a direct namespace update.

**21. Run the namespace-handover system workflow on c1**

```bash
temporal --address 127.0.0.1:7233 workflow start \
  --namespace temporal-system \
  --task-queue default-worker-tq \
  --type namespace-handover \
  --input '{"Namespace": "replicationtest", "RemoteCluster": "c2", "AllowedLaggingSeconds": 120, "HandoverTimeoutSeconds": 5}'
```

**22. Verify active cluster is now c2**

```bash
temporal --address 127.0.0.1:7233 operator namespace describe replicationtest \
  -o json | jq .replicationConfig.activeClusterName
# expected: "c2"
```

> **Note:** If this still shows `"c1"` immediately after the handover workflow completes, wait up to 60 seconds and retry. The namespace registry on each server polls for changes on a 60s interval (`dynamicConfigClient.pollInterval`). The handover itself is done — the cache just hasn't refreshed yet.

**23. Switch clients and workers to c2**

Point your SDK workers and clients at `127.0.0.1:2233`. Both clusters have `dcRedirectionPolicy: all-apis-forwarding`, so signals and starts sent to c1 will continue to be forwarded to c2 in the interim — but workers polling c1 will also be forwarded. Stop c1 workers before or immediately after switching DNS/addresses to avoid c1 workers picking up tasks meant for c2.

> **Migration vs long-running dual-cluster:** This walkthrough follows the migration/decommission path — c1 is being torn down after failover, so poll forwarding from c1 to c2 is harmless for the short window before shutdown. If instead you intend to keep both clusters running long-term (so the namespace can fail back to c1 in the future), do **not** proceed to Phase 7. Instead, enforce standby worker isolation on c1 by adding one of the following to `dynamicconfig/development-c1.yaml`:
>
> ```yaml
> # Option 1 — no forwarding at all from c1 (clients hitting c1 get NamespaceNotActive)
> system.enableNamespaceNotActiveAutoForwarding:
>   - value: false
>     constraints:
>       namespace: replicationtest
>
> # Option 2 — forward writes (signals, starts) but never polls from c1
> system.forceNamespaceSelectedAPIAutoForwarding:
>   - value: true
>     constraints:
>       namespace: replicationtest
> ```
>
> After a fail-back to c1, apply the same change on c2 and remove it from c1.

---

### Phase 7 — Decommission c1

**24. Remove c1 from the namespace replication config**

```bash
temporal --address 127.0.0.1:2233 operator namespace update \
  --namespace replicationtest \
  --cluster c2
```

**25. Verify only c2 remains in replication config**

```bash
temporal --address 127.0.0.1:2233 operator namespace describe \
  --namespace replicationtest -o json | jq .replicationConfig
```

**26. Delete the namespace on c1**

```bash
temporal --address 127.0.0.1:7233 operator namespace delete \
  --namespace replicationtest
```

**27. Verify namespace is gone from c1, still present on c2**

- http://localhost:8081/namespaces/replicationtest — should 404
- http://localhost:8082/namespaces/replicationtest — should load

**28. Disconnect the clusters**

```bash
temporal --address 127.0.0.1:7233 operator cluster remove --name c2
temporal --address 127.0.0.1:2233 operator cluster remove --name c1
```

**29. Verify c2 no longer references c1**

```bash
temporal --address 127.0.0.1:2233 operator cluster describe -o json
```

**30. Complete running executions on c2**

If you used the Java sample from Phase 2, run the worker pointed at c2 to pick up and complete the 10 running executions:

https://gist.github.com/tsurdilo/4114521b617016b5a5872ebf50e1494b

c1 can now be decommissioned.

## Version Upgrades

This setup runs a custom-built Temporal server image from local source (`~/devel/temporal/temporal`). Upgrading means updating that source checkout to the target version, aligning the admin-tools image, running any schema migrations, and rebuilding.

---

## Setup

- Persistence: `postgres12`
- Visibility: `postgres12`
- Docker network: `temporal-network`
- PostgreSQL container: `temporal-postgresql`
- PostgreSQL user: `temporal`, password: `temporal`
- Server image: custom-built from `~/devel/temporal/temporal` source
- Admin tools image: `temporalio/admin-tools:<version>` (set via `TEMPORAL_ADMINTOOLS_IMG` in `.env`)

---

## Step 1 — Stop the cluster

```bash
docker compose -f compose-postgres.yml -f compose-services.yml down --remove-orphans
```

---

## Step 2 — Update server source to target version

Check out the target release tag in the Temporal server source:

```bash
cd ~/devel/temporal/temporal
git fetch --tags
git checkout v1.32.0   # replace with target version
```

---

## Step 3 — Update admin-tools image version

In `.env`, set `TEMPORAL_ADMINTOOLS_IMG` to the target version:

```
TEMPORAL_ADMINTOOLS_IMG=1.32.0
```

The admin-tools version must match the server source version so schema migration scripts are aligned.

---

## Step 4 — Check current schema versions

```bash
docker exec -it temporal-postgresql psql -U temporal -d temporal -c "SELECT curr_version FROM schema_version;"
docker exec -it temporal-postgresql psql -U temporal -d temporal_visibility -c "SELECT curr_version FROM schema_version;"
```

Note both versions — these are your baseline before any migration.

---

## Step 5 — Check if schema migration is needed

Compare your current schema versions (from Step 4) against the highest version available in the target admin-tools image:

```bash
docker run --rm temporalio/admin-tools:1.32.0 \
  ls /etc/temporal/schema/postgresql/v12/temporal/versioned

docker run --rm temporalio/admin-tools:1.32.0 \
  ls /etc/temporal/schema/postgresql/v12/visibility/versioned
```

If your current schema version is already at the highest version listed — no migration needed, skip to Step 7. If higher versions are present — proceed to Step 6.

> **Note:** The `ls` output sorts lexicographically, not numerically — so `v1.19` appears between `v1.18` and `v1.2` in the listing. Find the highest numeric version manually, do not rely on the last entry in the listing.

---

## Step 6 — Run schema migrations

Schema must be updated **before** rolling the binary. Use the target version admin-tools image:

```bash
# Primary DB
docker run --rm \
  --network temporal-network \
  -e SQL_PASSWORD=temporal \
  temporalio/admin-tools:1.32.0 \
  temporal-sql-tool \
  --plugin postgres12 \
  --ep postgresql \
  -u temporal \
  -p 5432 \
  --db temporal \
  update-schema \
  --schema-dir /etc/temporal/schema/postgresql/v12/temporal/versioned

# Visibility DB
docker run --rm \
  --network temporal-network \
  -e SQL_PASSWORD=temporal \
  temporalio/admin-tools:1.32.0 \
  temporal-sql-tool \
  --plugin postgres12 \
  --ep postgresql \
  -u temporal \
  -p 5432 \
  --db temporal_visibility \
  update-schema \
  --schema-dir /etc/temporal/schema/postgresql/v12/visibility/versioned
```

---

## Step 7 — Rebuild and start

Rebuild the server image from the updated source and restart all services:

```bash
docker compose -f compose-services.yml up --build -d
```

Docker Compose detects the `build:` stanza on the Temporal service containers and rebuilds from `~/devel` as the build context. PostgreSQL is left running.

---

## Step 8 — Verify upgrade

```bash
temporal operator cluster describe -o json
```

Confirm `serverVersion` shows the target version.

Then verify both DBs are at the expected schema versions:

```bash
docker exec -it temporal-postgresql psql -U temporal -d temporal -c "SELECT curr_version FROM schema_version;"
docker exec -it temporal-postgresql psql -U temporal -d temporal_visibility -c "SELECT curr_version FROM schema_version;"
```

---

## Plaintext Payload Interceptor

The custom server in [`server/`](server/) includes a frontend gRPC interceptor that detects unencrypted payloads flowing through the Temporal frontend. It is observe-only — every request is always allowed through.

**What it does:** when any frontend API call carries a payload whose `encoding` metadata is `json/plain` or `binary/plain` (i.e., no payload codec is in use), the interceptor:

1. Increments a Prometheus counter `plaintext_payload_detected_total` tagged with `namespace`, `operation`, `encoding`, and where available `workflowType` and `taskqueue`. The `payload_field` tag identifies which field inside the request held the unencrypted payload — e.g. `input` on a `StartWorkflowExecution` means workflow input args were unencrypted, while `ScheduleActivityTask` on a `RespondWorkflowTaskCompleted` means activity input inside a worker command was unencrypted. This distinction matters because client-side and worker-side unencrypted traffic require different fixes from the tenant.
2. Emits a structured `WARN` log line with the same fields.

This gives cluster operators visibility into which tenants and workflow types are still sending unencrypted data, without breaking any existing traffic. The intended use is to run it in observe-only mode, alert on the metric, give tenants time to deploy a payload codec, then optionally harden it into a blocking gate.

**Implementation:** [`server/interceptors/plaintext_payload.go`](server/interceptors/plaintext_payload.go)

**Wiring into the server:** [`server/main.go`](server/main.go) — the interceptor is instantiated after the shared metrics handler is created and passed to `temporal.WithChainedFrontendGrpcInterceptors`:

```go
plainTextInterceptor := interceptors.NewPlainTextPayloadInterceptor(logger, metricsHandler)

temporal.NewServer(
    // ...
    temporal.WithChainedFrontendGrpcInterceptors(plainTextInterceptor.Intercept),
)
```

**Running the tests** (requires the local `go.work` in `server/` which redirects the Docker module paths to your local checkouts):

```bash
cd server && go test ./interceptors/ -v
```

See [`server/interceptors/README.md`](server/interceptors/README.md) for the full list of covered APIs, all metric tag names, PromQL queries for Grafana, and instructions for extending it to block requests.

## MinIO Archival

The custom server in [`server/`](server/) includes a status-filtered archival provider backed by [MinIO](https://min.io/) — an S3-compatible object store that runs as a container in the compose stack. Both workflow history and visibility records are archived to MinIO, gzip-compressed, when a workflow closes.

Archival is **enabled by default** (`USE_MINIO_ARCHIVAL=true` in `.env`). To disable it, set `USE_MINIO_ARCHIVAL=false` before starting the stack.

The provider is wired into the server via `temporal.WithCustomHistoryArchiverFactory` and `temporal.WithCustomVisibilityArchiverFactory` in [`server/main.go`](server/main.go). It registers the `minio://` URI scheme. The server config template at [`template/my_config_template.yaml`](template/my_config_template.yaml) conditionally enables the archival block when `USE_MINIO_ARCHIVAL` is set; `TEMPORAL_SERVER_CONFIG_FILE_PATH` in `compose-services.yml` tells each server container to load this template instead of the compiled-in default.

On first start, `minio-init` creates two buckets automatically:

- `temporal-history` — one object per workflow execution (key: `history/{namespaceID}/{workflowID}/{runID}_{failoverVersion}.history.gz`)
- `temporal-visibility` — one object per execution, date-partitioned by close time (key: `visibility/{namespaceID}/{YYYY}/{MM}/{DD}/{closeTimeNano}_{shortRunID}.visibility.gz`)

The [MinIO console](http://localhost:9011) (credentials: `minioadmin` / `minioadmin`) lets you browse both buckets.

### Namespace archival state

The `namespaceDefaults.archival` block in the server config is applied **only at namespace creation time** — it has no effect on existing namespaces. Once a namespace exists, its archival state (`HistoryArchivalState`, `VisibilityArchivalState`, and the URIs) is stored in the database and loaded from there on every startup. Static config is never consulted again for existing namespaces.

What this means in practice:

- **Fresh start (empty DB):** `temporal-admin-tools` creates the `default` namespace after the server is up. Because `namespaceDefaults.archival` in the rendered config has `state: enabled` with the minio URIs, the namespace is created with archival already enabled.
- **Restart with a persistent DB:** the namespace DB record already has archival enabled from its original creation — the server loads it from DB as-is, no extra step.
- **Namespace created before archival was enabled:** the DB record has `HistoryArchivalState: disabled`. Restarting with `USE_MINIO_ARCHIVAL=true` does not update it. `temporal-admin-tools` handles this by running the following update on every start when `USE_MINIO_ARCHIVAL=true` (see `script/setup.sh`):

```bash
temporal operator namespace update -n default \
    --history-archival-state enabled \
    --history-uri "minio://temporal-history/history" \
    --visibility-archival-state enabled \
    --visibility-uri "minio://temporal-visibility/visibility"
```

If archival is not showing as enabled after the stack comes up, verify the namespace state and run the command above manually if needed:

```bash
temporal operator namespace describe -n default | grep -i archival
```

### Querying archived workflows

```bash
# list all archived workflows
temporal workflow list --archived -n default

# filter by execution status
temporal workflow list --archived -n default --query "ExecutionStatus = 'Completed'"
temporal workflow list --archived -n default --query "ExecutionStatus = 'Failed'"
temporal workflow list --archived -n default --query "ExecutionStatus = 'Terminated'"
temporal workflow list --archived -n default --query "ExecutionStatus = 'TimedOut'"
temporal workflow list --archived -n default --query "ExecutionStatus = 'Canceled'"
temporal workflow list --archived -n default --query "ExecutionStatus = 'ContinuedAsNew'"

# retrieve full event history of an archived workflow
temporal workflow show -n default --workflow-id <id> --run-id <run-id>
```

Decompression is transparent — the CLI, SDK, and UI all receive normal uncompressed responses.

### Controlling which statuses are archived

Edit the `allowedStatuses` list in [`template/my_config_template.yaml`](template/my_config_template.yaml) under `archival.history.provider.customStores.minio` and the matching visibility block. An empty list (`[]`) archives all terminal statuses. Add specific values (e.g. `"Failed"`, `"Terminated"`) to restrict archival to only those statuses.

For full implementation details, object key layout, known limitations, and instructions for testing archival locally with reduced delays, see [`server/archiver/README.md`](server/archiver/README.md).