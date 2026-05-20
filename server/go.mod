module github.com/temporalio/temporal-custom-server

go 1.26.2

require (
	github.com/temporalio/temporal-etcd-dynconfig v0.0.0-00010101000000-000000000000
	github.com/urfave/cli/v2 v2.27.7
	go.temporal.io/server v0.0.0-00010101000000-000000000000
)

replace (
	go.temporal.io/server                         => /deps/temporal
	github.com/temporalio/temporal-etcd-dynconfig => /deps/temporal-etcd-dynconfig
)
