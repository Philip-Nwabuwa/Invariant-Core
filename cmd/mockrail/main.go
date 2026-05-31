// Command mockrail simulates the NIP rail with seedable chaos. It boots shared
// observability + /healthz and serves the RailService gRPC surface. Outcomes are
// deterministic by MOCKRAIL_SEED + the transfer reference; the probability knobs
// inject latency, timeouts, declines, and duplicate success callbacks.
package main

import (
	"context"
	"log"
	"strconv"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	mockrailv1 "github.com/Philip-Nwabuwa/Invariant-Core/api/gen/mockrail/v1"
	switchv1 "github.com/Philip-Nwabuwa/Invariant-Core/api/gen/switch/v1"
	"github.com/Philip-Nwabuwa/Invariant-Core/internal/mockrail"
	"github.com/Philip-Nwabuwa/Invariant-Core/internal/serviceboot"
)

func main() {
	cfg := mockrail.Config{
		Latency:     time.Duration(envInt("MOCKRAIL_LATENCY_MS", 0)) * time.Millisecond,
		Seed:        int64(envInt("MOCKRAIL_SEED", 1)),
		PTimeout:    envFloat("MOCKRAIL_P_TIMEOUT", 0),
		PDecline:    envFloat("MOCKRAIL_P_DECLINE", 0),
		PDuplicate:  envFloat("MOCKRAIL_P_DUPLICATE", 0),
		PTSQTimeout: envFloat("MOCKRAIL_P_TSQ_TIMEOUT", 0),
	}

	// Duplicate-callback chaos needs a path back to the switch. It is opt-in: a
	// callback target is only wired when SWITCH_CALLBACK_TARGET is set, so the
	// default and unit tests need no switch.
	var cleanup func()
	if target := serviceboot.EnvOr("SWITCH_CALLBACK_TARGET", ""); target != "" {
		conn, err := grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			log.Fatalf("mockrail: switch callback client: %v", err)
		}
		cfg.Callback = &railCallbackSender{client: switchv1.NewSwitchServiceClient(conn)}
		cleanup = func() { _ = conn.Close() }
	}

	server := mockrail.NewServerWithConfig(cfg)

	if err := serviceboot.Run(serviceboot.Options{
		ServiceName: "mockrail",
		HealthAddr:  serviceboot.EnvOr("MOCKRAIL_HTTP_ADDR", ":8082"),
		GRPCAddr:    serviceboot.EnvOr("MOCKRAIL_ADDR", ":50053"),
		RegisterGRPC: func(s *grpc.Server) {
			mockrailv1.RegisterRailServiceServer(s, server)
		},
		Cleanup: cleanup,
	}); err != nil {
		log.Fatalf("mockrail: %v", err)
	}
}

// railCallbackSender delivers an asynchronous RailCallback to the switch.
type railCallbackSender struct {
	client switchv1.SwitchServiceClient
}

// SendCallback fires a single rail callback, fire-and-forget on its own context.
func (s *railCallbackSender) SendCallback(reference string, declined bool) {
	st := switchv1.CallbackStatus_CALLBACK_STATUS_SUCCESS
	if declined {
		st = switchv1.CallbackStatus_CALLBACK_STATUS_DECLINED
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = s.client.RailCallback(ctx, &switchv1.RailCallbackRequest{Reference: reference, Status: st})
}

func envInt(key string, def int) int {
	v, err := strconv.Atoi(serviceboot.EnvOr(key, strconv.Itoa(def)))
	if err != nil {
		return def
	}
	return v
}

func envFloat(key string, def float64) float64 {
	v, err := strconv.ParseFloat(serviceboot.EnvOr(key, strconv.FormatFloat(def, 'f', -1, 64)), 64)
	if err != nil {
		return def
	}
	return v
}
