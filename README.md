## Table of Contents

- [About](#about)
- [Deploying via auto setup](#deploying-via-auto-setup)
- [Deploying without auto setup](#deploying-without-auto-setup)
- [Docker cleanup commands](#docker-cleanup-commands)
- [Troubleshoot](#troubleshoot)

## About

This repo includes some experiments on self-deploying Temporal server via Docker Compose.
It can serve as reference to community for a number of Docker related 
deployment questions.

## Deploying via auto setup

Temporal builds include the [auto-setup](https://hub.docker.com/r/temporalio/auto-setup) image which is a convenience way to run
server in a single container. It also includes a [startup script](https://github.com/temporalio/docker-builds/blob/main/docker/auto-setup.sh) 
which set things up like schemas (default and visibility), 
default namespace, default search attributes etc.
The downside of using this image is that [all Temporal services run in a single container](https://github.com/temporalio/docker-builds/blob/main/docker/start-temporal.sh#L15)
and they 
cannot be individually scaled etc. This setup is typically not recommended in prod envs.

### How to start
In the main repo dir run:

    docker network create temporal-network
    docker compose -f docker-compose-postgres.yml -f docker-compose-auto-setup.yml up

## Check if it works
Bash into admin-tools container and run tctl (you can do this from your machine if you have tctl installed too)

    docker container ls --format "table {{.ID}}\t{{.Image}}\t{{.Names}}"

copy the id of the temporal-admin-tools container
    
    docker exec -it <admin tools container id>> bash 
    tctl cl h

you should see response:

    temporal.api.workflowservice.v1.WorkflowService: SERVING

We start postgres from a separate compose file but you don't have to and can combine them if you want.

By the way, if you want to docker exec into the postgres container do:

    docker exec -it <temporal-postgres container id> psql -U temporal
    \l

which should show the temporal and temporal_visiblity dbs

### What's all included?

* Postgresql for persistence
* Temporal server via auto-config (with server metrics enabled)
* Temporal Web UI
* Prometheus
* Grafana set up with default sdk, server, and basic docker system dashboards (login disabled via config)
* Fluentd sidecar writing server logs to ES
* Kibana to read/search/filter server logs from ES
* Health check for admintools container
* Portainer

### Client access
Temporal frontend role is exposed (gRPC) on 127.0.0.1:7233 (so all SDK samples should work w/o changes)

### Important links:

* [Server metrics (raw)](http://localhost:8000/metrics)
* [Prometheus targets (scrape points)](http://localhost:9090/targets)
* [Grafana (includes server, sdk, and docker dashboards)](http://localhost:8085/)
  * no login required
  * In order to scrape docker system metrics add "metrics-addr":"127.0.0.1:9323" to your docker daemon.js, on Mac this is located at ~/.docker/daemon.json
* [Web UI v2](http://localhost:8080/namespaces/default/workflows)
* [Web UI v1](http://localhost:8088/)
* [Kibana (for server logs)](http://localhost:5601/)
  * You have to create your index pattern:
    * 1. [Create Index page](http://localhost:5601/app/management/kibana/indexPatterns/create) to create index with value "fluentd-*"
    * 2. Select the @timestamp field
    * 3. Go to Analysis->Discover to view logs
    * Add filters for logs if needed
* [Portainer](http://localhost:9000/)
  * Note you will have to create an user the first time you log in
  * Yes it forces a longer password but whatever

## Deploying without auto setup

This setup targets more of production environments as it deploys each Temporal server role
(frontend, matching, history, worker) in individual containers. 
Each server role container exposes server metrics under its own port.
We still set up and configure persistence (default and visibility) but instead of using the auto-setup 
image which also starts the server in single container, we run the persistence setup script, as well as 
set up default namespace and default search attributes via sh script via the admin-tools image.
Note that bashing into the admin-tools image also gives you access to tctl as well as the temporal-tools for different
dbs. 

### How to start
In the main repo dir run:

    docker network create temporal-network
    docker compose -f docker-compose-postgres.yml -f docker-compose-services.yml up

## Check if it works
Same info applies as in the previous "Check if it works" section so not going to repeat it again.
If you read this far you get a little bonus here tho :) 

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

* Matching (via grpc-health-probe)

```
grpc-health-probe -addr=localhost:7235 -service=temporal.api.workflowservice.v1.MatchingService
```

* History via grpc-health-probe)

```
grpc-health-probe -addr=localhost:7234 -service=temporal.api.workflowservice.v1.HistoryService
```

### What's all included?

* Postgresql for persistence
* Temporal server with each role in own container
* Temporal Web UI
* Prometheus
* Grafana set up with default sdk, server, and basic docker system dashboards (login disabled via config)
* Portainer

### Client access
Temporal frontend role is exposed (gRPC) on 127.0.0.1:7233 (so all SDK samples should work w/o changes)

### Important links:

* Server metrics (raw)
  * [History Service](http://localhost:8000/metrics)
  * [Matching Service](http://localhost:8001/metrics)
  * [Frontend Service](http://localhost:8002/metrics)
  * [Worker Service](http://localhost:8003/metrics)
* [Prometheus targets (scrape points)](http://localhost:9090/targets)
* [Grafana (includes server, sdk, and docker dashboards)](http://localhost:8085/)
  * no login required
  * In order to scrape docker system metrics add "metrics-addr":"127.0.0.1:9323" to your docker daemon.js, on Mac this is located at ~/.docker/daemon.json
* [Web UI v2](http://localhost:8080/namespaces/default/workflows)
* [Web UI v1](http://localhost:8088/)
* [Portainer](http://localhost:9000/)
  * Note you will have to create an user the first time you log in
  * Yes it forces a longer password but whatever

## Docker cleanup commands
    docker system prune -a
    docker volume prune

## Troubleshoot

* "Not enough hosts to serve the request"
  * Can happen on startup when some temporal service container did not start up properly, run the docker compose command again typically fixes this
