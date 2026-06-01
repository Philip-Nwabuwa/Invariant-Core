package main

import (
	"context"
	"fmt"
	"io"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	switchv1 "github.com/Philip-Nwabuwa/Invariant-Core/api/gen/switch/v1"
	"github.com/Philip-Nwabuwa/Invariant-Core/internal/reconcile"
)

// sendFeedback closes the loop (FR-F1): for every pending_reversal exception it
// asks switchd to re-drive the stranded reversal over gRPC. The corrective
// endpoint is idempotent, so re-running reconcile against the same gap is safe.
// Per-reference outcomes are logged to w; one failed call does not abort the rest.
func sendFeedback(ctx context.Context, w io.Writer, switchAddr string, res reconcile.Result) error {
	conn, err := grpc.NewClient(switchAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("dial switchd %q: %w", switchAddr, err)
	}
	defer func() { _ = conn.Close() }()
	client := switchv1.NewSwitchServiceClient(conn)

	var sent, failed int
	for _, exc := range res.Exceptions {
		if exc.Category != reconcile.CategoryPendingReversal {
			continue
		}
		resp, err := client.CorrectiveReversal(ctx, &switchv1.CorrectiveReversalRequest{
			Reference: exc.Reference,
			Reason:    "recon pending_reversal",
		})
		if err != nil {
			failed++
			_, _ = fmt.Fprintf(w, "reconcile: feedback %s failed: %v\n", exc.Reference, err)
			continue
		}
		sent++
		_, _ = fmt.Fprintf(w, "reconcile: feedback %s -> state=%s requeued=%t\n", exc.Reference, resp.GetState(), resp.GetRequeued())
	}
	_, _ = fmt.Fprintf(w, "reconcile: feedback sent=%d failed=%d\n", sent, failed)
	if failed > 0 {
		return fmt.Errorf("reconcile: %d corrective call(s) failed", failed)
	}
	return nil
}
