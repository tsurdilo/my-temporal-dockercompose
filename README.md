## Temporal Docker Compose

* Temporal (with server metrics enabled)
* Temporal Web UI
* Prometheus
* Grafana set up with default sdk, server, and basic docker system dashboards (login disabled via config)
* Fluentd sidecar writing server logs to ES
* Kibana to read/search/filter server logs from ES
* Health check for admintools service

### Start

    docker-compose up

### Metrics http endpoint

* Server metrics
* [http://localhost:8000/metrics](http://localhost:8000/metrics)

### Prometheus

* Scrape point automatically set up
* [http://localhost:9090/targets](http://localhost:9090/targets)

### Grafana

* Default server, sdk metrics dashboards and basic docker system dashboard
* No login required
* In order to scrape docker system metrics you would have to add:
  "metrics-addr":"127.0.0.1:9323"
  to your docker daemon.json. On mac this is located at ~/.docker/daemon.json. 
* [http://localhost:8085/](http://localhost:8085/)

### Web UI v1

* [http://localhost:8088/](http://localhost:8088/)

### Web UI v2

* [http://localhost:8080/namespaces/default/workflows](http://localhost:8080/namespaces/default/workflows)

### Kibana

* [http://localhost:5601/](http://localhost:5601/)

Note: 
* You have to create an index pattern:
  * Go go [Create Index page](http://localhost:5601/app/management/kibana/indexPatterns/create) to create index with value "fluentd-*"
  * Select the @timestamp field 
* After its created go to Analysis->Discover to view logs 
* You can add filters for logs if you want

### Docker cleanup commands
    docker system prune -a
    docker volume prune
