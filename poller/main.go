package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	adminservice "go.temporal.io/server/api/adminservice/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	frontendAddr := getenv("FRONTEND_ADDR", "temporal-loadbalancing:7233")
	pollInterval := mustParseDuration(getenv("POLL_INTERVAL", "15s"))

	conn, err := grpc.NewClient(frontendAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("failed to connect: %v", err)
	}
	defer conn.Close()

	client := adminservice.NewAdminServiceClient(conn)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	log.Printf("starting DeepHealthCheck poller against %s, interval %s", frontendAddr, pollInterval)

	for {
		select {
		case <-ctx.Done():
			log.Println("poller stopped")
			return
		case <-ticker.C:
			pollCtx, pollCancel := context.WithTimeout(ctx, 10*time.Second)
			resp, err := client.DeepHealthCheck(pollCtx, &adminservice.DeepHealthCheckRequest{})
			pollCancel()
			if err != nil {
				log.Printf("DeepHealthCheck error: %v", err)
				continue
			}
			log.Printf("cluster state: %s", resp.State)
			for _, svc := range resp.Services {
				for _, host := range svc.Hosts {
					log.Printf("  service=%s host=%s state=%s", svc.Service, host.Address, host.State)
				}
			}
		}
	}
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func mustParseDuration(s string) time.Duration {
	d, err := time.ParseDuration(s)
	if err != nil {
		log.Fatalf("invalid duration %q: %v", s, err)
	}
	return d
}
