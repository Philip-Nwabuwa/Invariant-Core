// Command ledger is the double-entry ledger service (source of internal truth).
// It boots shared observability + /healthz, opens its Postgres pool, and serves
// the LedgerService gRPC surface (PostTransaction, GetBalance, GetAccount,
// ListEntries, ExportTransactions) on its gRPC port.
package main

import (
	"context"
	"log"
	"time"

	"google.golang.org/grpc"

	ledgerv1 "github.com/Philip-Nwabuwa/Invariant-Core/api/gen/ledger/v1"
	"github.com/Philip-Nwabuwa/Invariant-Core/internal/ledger"
	"github.com/Philip-Nwabuwa/Invariant-Core/internal/ledger/postgres"
	"github.com/Philip-Nwabuwa/Invariant-Core/internal/serviceboot"
	"github.com/Philip-Nwabuwa/Invariant-Core/pkg/metrics"
)

const defaultDBURL = "postgres://invariantcore:invariantcore@localhost:5432/invariantcore?sslmode=disable"

func main() {
	connectCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := postgres.NewPool(connectCtx, serviceboot.EnvOr("DB_URL", defaultDBURL))
	if err != nil {
		log.Fatalf("ledger: database: %v", err)
	}

	// The registry is owned here so the serialization-retry SLIs (NS-505) are
	// registered before boot and served on /metrics.
	reg := metrics.New()
	ledgerMetrics := ledger.NewMetrics(reg)
	server := ledger.NewGRPCServer(ledger.NewService(postgres.NewRepository(pool), ledger.WithMetrics(ledgerMetrics)))

	if err := serviceboot.Run(serviceboot.Options{
		ServiceName: "ledger",
		HealthAddr:  serviceboot.EnvOr("LEDGER_HTTP_ADDR", ":8081"),
		GRPCAddr:    serviceboot.EnvOr("LEDGER_GRPC_ADDR", ":50051"),
		Registry:    reg,
		RegisterGRPC: func(s *grpc.Server) {
			ledgerv1.RegisterLedgerServiceServer(s, server)
		},
		Cleanup: pool.Close,
	}); err != nil {
		log.Fatalf("ledger: %v", err)
	}
}
