package transfer

import (
	"context"
	"fmt"

	mockrailv1 "github.com/Philip-Nwabuwa/Invariant-Core/api/gen/mockrail/v1"
)

// RailClient adapts the mockrail gRPC client to the orchestrator's Rail
// interface. cmd/switchd dials mockrail and constructs one (NS-205).
type RailClient struct {
	client mockrailv1.RailServiceClient
}

// NewRailClient wraps a generated RailServiceClient.
func NewRailClient(client mockrailv1.RailServiceClient) *RailClient {
	return &RailClient{client: client}
}

// RailClient implements Rail — checked at compile time.
var _ Rail = (*RailClient)(nil)

// Send submits the transfer to the rail and fails on any non-success verdict.
func (r *RailClient) Send(ctx context.Context, t Transfer) error {
	resp, err := r.client.SendToRail(ctx, &mockrailv1.SendToRailRequest{
		Reference:   t.Reference,
		Source:      t.Source,
		Destination: t.Destination,
		AmountMinor: t.Amount.Minor(),
		Currency:    t.Currency,
	})
	if err != nil {
		return fmt.Errorf("rail send: %w", err)
	}
	if resp.GetStatus() != mockrailv1.RailStatus_RAIL_STATUS_SUCCESS {
		return fmt.Errorf("rail returned non-success status: %s", resp.GetStatus())
	}
	return nil
}
