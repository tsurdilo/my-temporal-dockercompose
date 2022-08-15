## Table of Contents

- [About](#about)
- [Compose: Deploying via auto setup](#deploying-via-auto-setup)
- [Compose: Deploying without auto setup](#deploying-without-auto-setup)
- [Swarm: Deploy on single node Swarm](#deploying-on-single-node-swarm)
- [Some usueful Docker commands](#some-useful-docker-commands)
- [Troubleshoot](#troubleshoot)

## About

This repo includes some experiments on self-deploying Temporal server via Docker 
Compose and Swarm.
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
  
## Deploying on single node Swarm

Init the Swarm capability if you haven't already

    docker swarm init

Create the overlay network

    docker network create --scope=swarm --driver=overlay --attachable temporal-network

(Optional) Create the visualizer service:

    docker service create \
      --name=viz \
      --publish=8050:8080/tcp \
      --constraint=node.role==manager \
      --mount=type=bind,src=/var/run/docker.sock,dst=/var/run/docker.sock \
      dockersamples/visualizer

Create the postgresql stack

    docker stack deploy -c docker-compose-postgres.yml temporal-postgres

Create the services stack

    docker stack deploy -c docker-compose-services.yml temporal-services

Check out your stacks

    docker stack ls

Check out your services

    docker service ls

Note they should all have mode "replicated" and 1 replica by default
(if they don't show that right away wait a sec or two and run this command again)

Inspect frontend service

    docker service inspect --pretty temporal-services_temporal-frontend

### Let's have some fun with Swarm


Let's scale the history service to 2 
(you can do this for other services too if you want to play around)

    docker service scale temporal-services_temporal-history=2

Run `docker service ls` again, you should see 2 replicas now for history node

### Todo

Still trying to figure out how to access frontend 7233 port outside of swarm.
It has something to do with port ingress and grpc but im not sure what yet.
If anyone knows let me know :) 

Right now you would need to deploy your temporal client service to 
swarm and set target temporal-frontend:7233 to connect and run workflows.
You can always bash into the admin-tools service and run tctl from there,
via Portainer or in your terminal.

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
* [Web UI v2](http://localhost:8081/namespaces/default/workflows)
* [Web UI v1](http://localhost:8088/)
* [Portainer](http://localhost:9000/)
  * Note you will have to create an user the first time you log in
  * Yes it forces a longer password but whatever
* [Swarm visualizer](http://localhost:8050/)

To leave swarm mode after your done you can do:

    docker swarm leave -f

## Some useful Docker commands
    docker-compose down --volumes
    docker system prune -a
    docker volume prune

## Troubleshoot

* "Not enough hosts to serve the request"
  * Can happen on startup when some temporal service container did not start up properly, run the docker compose command again typically fixes this
