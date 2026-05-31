// Command switchd is the transfer engine (the prevention guarantee). It boots
// shared observability + /healthz, serves the public REST transfer API on its
// HTTP port, and binds its internal gRPC port. It opens its Postgres pool and
// dials the ledger and mockrail gRPC services, then drives a real transfer
// through the orchestrator behind durable idempotency (NS-205).
package main

import (
	"context"
	"log"
	"net/http"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	ledgerv1 "github.com/Philip-Nwabuwa/Invariant-Core/api/gen/ledger/v1"
	mockrailv1 "github.com/Philip-Nwabuwa/Invariant-Core/api/gen/mockrail/v1"
	"github.com/Philip-Nwabuwa/Invariant-Core/internal/ledger/postgres"
	"github.com/Philip-Nwabuwa/Invariant-Core/internal/serviceboot"
	transfer "github.com/Philip-Nwabuwa/Invariant-Core/internal/switch"
	"github.com/Philip-Nwabuwa/Invariant-Core/internal/switch/transport"
)

const defaultDBURL = "postgres://invariantcore:invariantcore@localhost:5432/invariantcore?sslmode=disable"

func main() {
	connectCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := postgres.NewPool(connectCtx, serviceboot.EnvOr("DB_URL", defaultDBURL))
	if err != nil {
		log.Fatalf("switchd: database: %v", err)
	}

	// gRPC clients to the ledger and the rail. NewClient is lazy: it dials on the
	// first RPC, so a slow dependency at boot doesn't crash the switch.
	ledgerConn, err := grpc.NewClient(
		serviceboot.EnvOr("LEDGER_GRPC_TARGET", "localhost:50051"),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		log.Fatalf("switchd: ledger client: %v", err)
	}
	railConn, err := grpc.NewClient(
		serviceboot.EnvOr("MOCKRAIL_GRPC_TARGET", "localhost:50053"),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		log.Fatalf("switchd: rail client: %v", err)
	}

	ledgerClient := transfer.NewLedgerClient(ledgerv1.NewLedgerServiceClient(ledgerConn))
	railClient := transfer.NewRailClient(mockrailv1.NewRailServiceClient(railConn))

	orchestrator := transfer.NewOrchestrator(transfer.NewPostgresStore(pool), ledgerClient, railClient)
	svc := transfer.NewIdempotentService(orchestrator, transfer.NewIdempotencyStore(pool))
	handler := transport.NewHandler(svc)

	if err := serviceboot.Run(serviceboot.Options{
		ServiceName: "switchd",
		HealthAddr:  serviceboot.EnvOr("SWITCHD_HTTP_ADDR", ":8080"),
		GRPCAddr:    serviceboot.EnvOr("SWITCHD_GRPC_ADDR", ":50052"),
		// Mount the REST router at "/"; the more specific /healthz and /metrics
		// patterns registered by serviceboot still take precedence.
		RegisterHTTP: func(mux *http.ServeMux) {
			mux.Handle("/", handler.Routes())
		},
		Cleanup: func() {
			_ = ledgerConn.Close()
			_ = railConn.Close()
			pool.Close()
		},
	}); err != nil {
		log.Fatalf("switchd: %v", err)
	}
}
