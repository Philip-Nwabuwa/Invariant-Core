// Command ledger is the double-entry ledger service (source of internal truth).
// Sprint 0: boots, serves /healthz + /metrics, and binds its gRPC port with an
// empty surface. The PostTransaction/GetBalance API arrives in Sprint 1.
package main

import (
	"log"

	"github.com/Philip-Nwabuwa/Invariant-Core/internal/serviceboot"
)

func main() {
	if err := serviceboot.Run(serviceboot.Options{
		ServiceName: "ledger",
		HealthAddr:  serviceboot.EnvOr("LEDGER_HTTP_ADDR", ":8081"),
		GRPCAddr:    serviceboot.EnvOr("LEDGER_GRPC_ADDR", ":50051"),
	}); err != nil {
		log.Fatalf("ledger: %v", err)
	}
}
