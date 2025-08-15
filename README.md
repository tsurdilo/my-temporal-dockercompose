## Table of Contents

- [About](#about)
- [Deploying your Temporal service on Docker](#deploying-your-service)
- [Some usueful Docker commands](#some-useful-docker-commands)
- [Troubleshoot](#troubleshoot)
- [Extra](#extra)
  - [Multi Cluster Replication setup](#multi-cluster-replication-setup)
  
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
to get it up and running locally

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

## Multi Cluster Replication Setup

Clear your docker env (see "Some useful Docker commands" section)

Start the multicluster replication services

    docker network create temporal-network-replication
    docker compose -f compose-services-replication.yml up --detach   

This will start two separate Temporal clusters. Lets make sure they are up:

     temporal --address 127.0.0.1:7233 operator cluster health 

     temporal --address 127.0.0.1:2233 operator cluster health 

Let's also get their cluster names
    
     temporal --address 127.0.0.1:7233 operator cluster describe -o json | jq .clusterName

     temporal --address 127.0.0.1:2233 operator cluster describe -o json | jq .clusterName

Get the pod IPs of the two cluster services:

    docker inspect -f '{{range.NetworkSettings.Networks}}{{.IPAddress}}{{end}}' temporalc1

    docker inspect -f '{{range.NetworkSettings.Networks}}{{.IPAddress}}{{end}}' temporalc2

Write down these IPs for temporalc1, temporalc2 clusters, we are referencing them below as
TEMPORALC1_CLUSTER_IP, TEMPORALC2_CLUSTER_IP respectively

For this example setup we are going to create a non-global namespace on temporalc1 cluster, 
and run some workflows on it. Then we are going to connect the clusters and promote this namespace to global.
Once that is done we are going to start replications for this namespace for all executions (completed and running) 
And then simulate a "failover" scenario. 

Create a "replicationtest" namespace on our c1 cluster:

    temporal --address 127.0.0.1:7233 operator namespace create replicationtest
   
Run some executions on this namespace, to see full effect of replication best to run a good number of them and 
have some completed/failed and some also running, like long-running workflows that we can then continue on 
the c2 cluster once we fail over to it.

Here is one Java sample that you can run out of box to create 30 executions and complete 20, leaving 10 running that
can be used:

    https://gist.github.com/tsurdilo/f0ef3ea2940e877aaec7489370ae099c

Let's make sure our workflows are on the replication ns on c1 cluster:

    http://localhost:8081/namespaces/replicationtest/workflows

And are not on our c2 cluster as this namespace does not even exist there (yet):

    http://localhost:8082/namespaces/replicationtest/workflows

Ok let's start doing some work now:

Enable connection from temporalc1 cluster to temporalc2 cluster

    temporal operator cluster upsert --enable-connection --frontend-address "TEMPORALC2_CLUSTER_IP:2233"

Enable connection from temporalc2 cluster to temporalc1 cluster

    temporal --address 127.0.0.1:2233 operator cluster upsert --enable-connection --frontend-address "TEMPORAL_C1_CLUSTER_IP:7233" 

Now to start replication, we need to promote our replicationtest namespace on the c1 cluster from
local namespace to global namespace:

    temporal operator namespace update --namespace replicationtest --promote-global

Let's describe this namespace now to make sure its global

    temporal operator namespace describe --namespace replicationtest -o json | jq .isGlobalNamespace

Now our namespace is global and only on the c1 cluster. We now have to update namespace config of this now
global namespace to include both clusters

    temporal operator namespace update --namespace replicationtest --cluster c1 --cluster c2 

Let's make that we now have both clusters defined for this namespace:

    temporal operator namespace describe --namespace replicationtest -o json | jq .replicationConfig

This should have now also created (replicated) our replicationtest namespace on the c2 cluster, check it:

    http://localhost:8082/namespaces/replicationtest/workflows

There are not going to be any workflows however (yet). New workflows you start will be replicated..but we have 30 existing ones,
20 completed and 10 running on c1 cluster, what about those??

Let's force replicate those first

    temporal workflow start --namespace temporal-system --type force-replication --task-queue default-worker-tq --input '{ "Namespace": "replicationtest", "ConcurrentActivityCount": 4, "OverallRps": 80}'

Check if force-replication workflow completed

    http://localhost:8081/namespaces/temporal-system/workflows?query=WorkflowType%3D%22force-replication%22

Now check the c2 cluster to make sure all our completed and running executons were replicated

    http://localhost:8082/namespaces/replicationtest/workflows

You should see the 20 completed executions and the 10 running ones

Ok so at this point what we want to do is make c2 our primary and only cluster.

First we need to enable forwarding. In c2 cell dynamic config lets add:

       system.enableNamespaceNotActiveAutoForwarding:
          - value: true

to c1 dynamic config in /dynamicconfig/development-c1.yaml in this repo.

Next we run the namespace-handover workflow to make our replicationtest namespace active in c2 only

    temporal workflow start --namespace temporal-system --task-queue default-worker-tq --type namespace-handover --input '{ "Namespace": "replicationtest", "RemoteCluster": "c2", "AllowedLaggingSeconds": 120, "HandoverTimeoutSeconds": 5}'

Let's check if this worked by describing our replicationtest namespace on c1 cluster:

    temporal operator namespace describe replicationtest -o json | jq .replicationConfig.activeClusterName

should show "c2"

At this point you would start changing your DNS to point all clients and workers to cluster c2. 
This is not something that we are doing here, but its something you can do yourself given how you do your deployments.

Last step is now to start disconnecting c1 cluster from c2 and removing our replicationtest namespace in c1 as we 
don't need it any more.

First remove c1 in namespace config for our replicationtest namespace:

    temporal operator namespace update --namespace replicationtest --cluster c2

Check to make sure c1 is no longer available:

    temporal operator namespace describe --namespace replicationtest -o json | jq .replicationConfig

Delete our namespace in cell c1

    temporal operator namespace delete --namespace replicationtest   

Lets check that this namespace is no longer there in c1 but is in c2

    http://localhost:8081/namespaces/replicationtest

    http://localhost:8082/namespaces/replicationtest

Last step is to disconnect c1 and c2 completely

     temporal operator cluster remove --name c2   

     temporal --address 127.0.0.1:2233 operator cluster remove --name c1

Let's just check that c2 no longer has reference to c1

    temporal --address 127.0.0.1:2233 operator cluster describe -o json

So now we can decomission our c1 cluster and can complete all workflows on c2 cluster which is now our primary one.

For the very last thing, we can just now swich our worker to c2 cluster and complete the running executions. 
If you ran the first Java class to start these workflows on c1, now you can just run this one:

     https://gist.github.com/tsurdilo/4114521b617016b5a5872ebf50e1494b

The end...for now ;) 