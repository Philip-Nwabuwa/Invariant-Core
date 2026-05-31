package transfer

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	mockrailv1 "github.com/Philip-Nwabuwa/Invariant-Core/api/gen/mockrail/v1"
)

// RailClient adapts the mockrail gRPC client to the orchestrator's Rail
// interface. cmd/switchd dials mockrail and constructs one.
type RailClient struct {
	client mockrailv1.RailServiceClient
}

// NewRailClient wraps a generated RailServiceClient.
func NewRailClient(client mockrailv1.RailServiceClient) *RailClient {
	return &RailClient{client: client}
}

// RailClient implements Rail — checked at compile time.
var _ Rail = (*RailClient)(nil)

// Send submits the transfer to the rail and classifies the outcome:
//   - a SUCCESS verdict -> VerdictSuccess;
//   - a DECLINED (or any non-success) verdict -> VerdictDeclined;
//   - a deadline/timeout -> VerdictUnknown (in-doubt: we lost the answer, not
//     necessarily the transfer — the driver resolves it with a TSQ);
//   - any other transport error is returned so the outbox retries the send.
func (r *RailClient) Send(ctx context.Context, t Transfer) (RailVerdict, error) {
	resp, err := r.client.SendToRail(ctx, &mockrailv1.SendToRailRequest{
		Reference:   t.Reference,
		Source:      t.Source,
		Destination: t.Destination,
		AmountMinor: t.Amount.Minor(),
		Currency:    t.Currency,
	})
	if err != nil {
		if isIndeterminate(err) {
			return VerdictUnknown, nil
		}
		return VerdictUnknown, fmt.Errorf("rail send: %w", err)
	}
	if resp.GetStatus() == mockrailv1.RailStatus_RAIL_STATUS_SUCCESS {
		return VerdictSuccess, nil
	}
	return VerdictDeclined, nil
}

// isIndeterminate reports whether err means the rail outcome is unknown (a
// timeout), as opposed to a connection error the caller should retry.
func isIndeterminate(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var se interface{ GRPCStatus() *status.Status }
	if errors.As(err, &se) {
		return se.GRPCStatus().Code() == codes.DeadlineExceeded
	}
	return false
}
