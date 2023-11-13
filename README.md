## Table of Contents

- [About](#about)
- [Deploying your Temporal service on Docker](#deploying-your-service)
- [Some usueful Docker commands](#some-useful-docker-commands)
- [Troubleshoot](#troubleshoot)
- [Extra](#extra)
  - [Swarm: Deploy on single node Swarm](#deploying-on-single-node-swarm)
  - [Compose: Temporalite](#deploying-temporalite)

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

Then in the main repo dir run:

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

## Troubleshoot

* "Not enough hosts to serve the request"
  * Can happen on startup when some temporal service container did not start up properly, run the docker compose command again typically fixes this

## Extra
Here are some extra configurations, try them out and please report any errors.

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

    docker stack deploy -c compose-postgres.yml temporal-postgres

Create the services stack

    docker stack deploy -c compose-services.yml temporal-services

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
* [Grafana (includes server, sdk, docker, and postgres dashboards)](http://localhost:8085/)
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

## Deploying Temporalite

[Temporalite](https://github.com/temporalio/temporalite) is a
is a distribution of Temporal that runs as a single process with zero runtime dependencies.
It includes both disk and in-memory modes via SQLite.

At the time of writing there is no official Temporalite image on dockerhub but you can easily build it yourself.
Also at time of writing Temporalite does not expose server metrics.

### Building Temporalite image manually

    git clone git@github.com:temporalio/temporalite.git
    cd temporalite
    docker build -t <your_tag>/temporalite .

For this sample the <your_tag> is called "tsurdilo". You can change it and update the corresponding
image in compose-temporalite.yml

### Deploying via Compose

    docker network create temporal-network
    docker compose -f compose-temporalite.yml up

Note the entry point specified in its docker file [here](https://github.com/temporalio/temporalite/blob/main/Dockerfile#L16)
You can try playing with the options if you want. For this demo we just assume default entry point options are uses as
defined there.

### Building Temporalite image with Docker

This option still builds the image but instead of us building manually utilizes the docker compose "build" tag to have
Docker build it from github repo.

### Deploying via Compose

    docker network create temporal-network
    docker compose -f compose-temporalite2.yml up

### What's all included?

* Temporalite (ephemeral - in memory). Note you will lose all your data when container restarts
* Web UI
* Admin Tools

### Important links:

* [Web UI](http://localhost:8233/)

