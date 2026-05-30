// Command mockrail simulates the NIP rail with seedable chaos. Sprint 0: boots,
// serves /healthz + /metrics, and binds its primary listener with an empty
// surface. Latency/timeout/duplicate/decline injection arrives in Sprints 2-3.
package main

import (
	"log"

	"github.com/Philip-Nwabuwa/Invariant-Core/internal/serviceboot"
)

func main() {
	if err := serviceboot.Run(serviceboot.Options{
		ServiceName: "mockrail",
		HealthAddr:  serviceboot.EnvOr("MOCKRAIL_HTTP_ADDR", ":8082"),
		GRPCAddr:    serviceboot.EnvOr("MOCKRAIL_ADDR", ":50053"),
	}); err != nil {
		log.Fatalf("mockrail: %v", err)
	}
}
