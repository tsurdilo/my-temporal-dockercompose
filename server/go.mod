module github.com/temporalio/temporal-custom-server

go 1.26.2

require (
	github.com/temporalio/temporal-etcd-dynconfig v0.0.0-00010101000000-000000000000
	github.com/urfave/cli/v2 v2.27.7
	go.temporal.io/api v1.62.12-0.20260430203359-15c391664683
	go.temporal.io/server v0.0.0-00010101000000-000000000000
	google.golang.org/grpc v1.80.0
)

replace (
	go.temporal.io/server                         => /deps/temporal
	github.com/temporalio/temporal-etcd-dynconfig => /deps/temporal-etcd-dynconfig
)
