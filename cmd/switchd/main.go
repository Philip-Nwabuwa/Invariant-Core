// Command switchd is the transfer engine (the prevention guarantee). Sprint 0:
// boots, serves /healthz + /metrics on the public HTTP port, and binds its
// internal gRPC port with an empty surface. The REST transfer API and state
// machine arrive in Sprint 2.
package main

import (
	"log"

	"github.com/Philip-Nwabuwa/Invariant-Core/internal/serviceboot"
)

func main() {
	if err := serviceboot.Run(serviceboot.Options{
		ServiceName: "switchd",
		HealthAddr:  serviceboot.EnvOr("SWITCHD_HTTP_ADDR", ":8080"),
		GRPCAddr:    serviceboot.EnvOr("SWITCHD_GRPC_ADDR", ":50052"),
	}); err != nil {
		log.Fatalf("switchd: %v", err)
	}
}
