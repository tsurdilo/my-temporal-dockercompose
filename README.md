## Temporal Docker Compose

* Temporal (with server metrics enabled)
* Temporal Web UI
* Prometheus set up
* Grafana set up with default sdk and server dashboards and no login required

### Metrics http endpoint

* [http://localhost:8000/metrics](http://localhost:8000/metrics)

### Prometheus

* [http://localhost:9090/targets](http://localhost:9090/targets)

### Grafana

* [http://localhost:8085/](http://localhost:8085/)

### Web UI

* [http://localhost:8088/](http://localhost:8088/)

### Docker cleanup commands
    docker system prune -a
    docker volume prune