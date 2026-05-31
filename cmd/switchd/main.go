// Command switchd is the transfer engine (the prevention guarantee). It boots
// shared observability + /healthz, serves the public REST transfer API on its
// HTTP port, and binds its internal gRPC port. NS-201 wires the REST surface
// against a stub service; the real orchestrator (rail + ledger) lands in
// NS-203/205.
package main

import (
	"log"
	"net/http"

	"github.com/Philip-Nwabuwa/Invariant-Core/internal/serviceboot"
	transfer "github.com/Philip-Nwabuwa/Invariant-Core/internal/switch"
	"github.com/Philip-Nwabuwa/Invariant-Core/internal/switch/transport"
)

func main() {
	handler := transport.NewHandler(transfer.NewStubService())

	if err := serviceboot.Run(serviceboot.Options{
		ServiceName: "switchd",
		HealthAddr:  serviceboot.EnvOr("SWITCHD_HTTP_ADDR", ":8080"),
		GRPCAddr:    serviceboot.EnvOr("SWITCHD_GRPC_ADDR", ":50052"),
		// Mount the REST router at "/"; the more specific /healthz and /metrics
		// patterns registered by serviceboot still take precedence.
		RegisterHTTP: func(mux *http.ServeMux) {
			mux.Handle("/", handler.Routes())
		},
	}); err != nil {
		log.Fatalf("switchd: %v", err)
	}
}
