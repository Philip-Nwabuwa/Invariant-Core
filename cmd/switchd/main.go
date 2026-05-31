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

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	ledgerv1 "github.com/Philip-Nwabuwa/Invariant-Core/api/gen/ledger/v1"
	mockrailv1 "github.com/Philip-Nwabuwa/Invariant-Core/api/gen/mockrail/v1"
	"github.com/Philip-Nwabuwa/Invariant-Core/internal/ledger/postgres"
	"github.com/Philip-Nwabuwa/Invariant-Core/internal/serviceboot"
	transfer "github.com/Philip-Nwabuwa/Invariant-Core/internal/switch"
	"github.com/Philip-Nwabuwa/Invariant-Core/internal/switch/outbox"
	"github.com/Philip-Nwabuwa/Invariant-Core/internal/switch/transport"
	"github.com/Philip-Nwabuwa/Invariant-Core/pkg/logging"
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
	// first RPC, so a slow dependency at boot doesn't crash the switch. The
	// otelgrpc stats handler emits a child span per call (so the transfer is one
	// trace), and the correlation interceptor carries the request's correlation
	// id across the wire as metadata.
	clientOpts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
		grpc.WithUnaryInterceptor(logging.UnaryClientInterceptor()),
	}
	ledgerConn, err := grpc.NewClient(serviceboot.EnvOr("LEDGER_GRPC_TARGET", "localhost:50051"), clientOpts...)
	if err != nil {
		log.Fatalf("switchd: ledger client: %v", err)
	}
	railConn, err := grpc.NewClient(serviceboot.EnvOr("MOCKRAIL_GRPC_TARGET", "localhost:50053"), clientOpts...)
	if err != nil {
		log.Fatalf("switchd: rail client: %v", err)
	}

	ledgerClient := transfer.NewLedgerClient(ledgerv1.NewLedgerServiceClient(ledgerConn))
	railClient := transfer.NewRailClient(mockrailv1.NewRailServiceClient(railConn))

	store := transfer.NewPostgresStore(pool)
	driver := transfer.NewDriver(store, ledgerClient, railClient)
	orchestrator := transfer.NewOrchestrator(store, driver)
	svc := transfer.NewIdempotentService(orchestrator, transfer.NewIdempotencyStore(pool))
	handler := transport.NewHandler(svc)

	// The outbox poller drives every post-debit step (rail, settlement, reversal)
	// asynchronously, so a crash mid-flow resumes from the durable event log
	// rather than losing the work. It runs until shutdown cancels pollCtx.
	pollCtx, stopPoller := context.WithCancel(context.Background())
	poller := outbox.NewPoller(store.Queries(), driver, outbox.Config{})
	go poller.Run(pollCtx)

	if err := serviceboot.Run(serviceboot.Options{
		ServiceName: "switchd",
		HealthAddr:  serviceboot.EnvOr("SWITCHD_HTTP_ADDR", ":8080"),
		GRPCAddr:    serviceboot.EnvOr("SWITCHD_GRPC_ADDR", ":50052"),
		// Mount the REST router at "/"; the more specific /healthz and /metrics
		// patterns registered by serviceboot still take precedence. otelhttp
		// starts the root server span for each transfer request, which the
		// orchestrator's gRPC calls then continue into one trace.
		RegisterHTTP: func(mux *http.ServeMux) {
			mux.Handle("/", otelhttp.NewHandler(handler.Routes(), "switchd.rest"))
		},
		Cleanup: func() {
			stopPoller()
			_ = ledgerConn.Close()
			_ = railConn.Close()
			pool.Close()
		},
	}); err != nil {
		log.Fatalf("switchd: %v", err)
	}
}
