// Command mockrail simulates the NIP rail with seedable chaos. It boots shared
// observability + /healthz and serves the RailService gRPC surface. NS-204
// implements the success path (configurable latency); chaos arrives in Sprint 3.
package main

import (
	"log"
	"strconv"
	"time"

	"google.golang.org/grpc"

	mockrailv1 "github.com/Philip-Nwabuwa/Invariant-Core/api/gen/mockrail/v1"
	"github.com/Philip-Nwabuwa/Invariant-Core/internal/mockrail"
	"github.com/Philip-Nwabuwa/Invariant-Core/internal/serviceboot"
)

func main() {
	server := mockrail.NewServer(latencyFromEnv())

	if err := serviceboot.Run(serviceboot.Options{
		ServiceName: "mockrail",
		HealthAddr:  serviceboot.EnvOr("MOCKRAIL_HTTP_ADDR", ":8082"),
		GRPCAddr:    serviceboot.EnvOr("MOCKRAIL_ADDR", ":50053"),
		RegisterGRPC: func(s *grpc.Server) {
			mockrailv1.RegisterRailServiceServer(s, server)
		},
	}); err != nil {
		log.Fatalf("mockrail: %v", err)
	}
}

// latencyFromEnv reads MOCKRAIL_LATENCY_MS (milliseconds); an unset or invalid
// value means no added latency.
func latencyFromEnv() time.Duration {
	ms, err := strconv.Atoi(serviceboot.EnvOr("MOCKRAIL_LATENCY_MS", "0"))
	if err != nil || ms < 0 {
		return 0
	}
	return time.Duration(ms) * time.Millisecond
}
