// Package chaos_test is the AC-1 acceptance test: drive N transfers through the
// real switch stack under deterministic mockrail chaos (timeouts, declines,
// duplicate callbacks) plus a mid-flow crash, and assert ZERO stranded debits —
// every debit ends matched by a credit (settled), a completed reversal, or money
// held in suspense under MANUAL_REVIEW (a flagged, known position — never a
// silent stranded debit).
//
// The harness runs everything in-process: a real ledger gRPC server over bufconn,
// the real mockrail simulator over bufconn, and the real Postgres-backed
// orchestrator/driver/outbox. The "kill switchd mid-flow" is modelled by deleting
// the in-flight outbox events while every transfer sits at DEBITED (the canonical
// stranded-debit window) — on restart the recovery sweep re-enqueues them and the
// poller drives each to its true, seed-determined terminal state.
//
// No build tag: this runs under `go test ./...` and skips when Docker is absent,
// matching the repo's other testcontainers integration tests.
package chaos_test

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	ledgerv1 "github.com/Philip-Nwabuwa/Invariant-Core/api/gen/ledger/v1"
	mockrailv1 "github.com/Philip-Nwabuwa/Invariant-Core/api/gen/mockrail/v1"
	"github.com/Philip-Nwabuwa/Invariant-Core/internal/ledger"
	ledgerpg "github.com/Philip-Nwabuwa/Invariant-Core/internal/ledger/postgres"
	"github.com/Philip-Nwabuwa/Invariant-Core/internal/ledger/postgres/ledgerdb"
	"github.com/Philip-Nwabuwa/Invariant-Core/internal/ledger/postgres/testsupport"
	"github.com/Philip-Nwabuwa/Invariant-Core/internal/mockrail"
	transfer "github.com/Philip-Nwabuwa/Invariant-Core/internal/switch"
	"github.com/Philip-Nwabuwa/Invariant-Core/internal/switch/outbox"
	"github.com/Philip-Nwabuwa/Invariant-Core/pkg/money"
)

const (
	srcAccount   = "CUST-001"
	dstAccount   = "CUST-002"
	suspenseAcct = "SETTLEMENT"
)

// chaosConfig is the rail chaos used by the test. Probabilities + seed are fixed
// so the outcome split is reproducible (the rail derives every verdict from
// hash(seed, reference, dimension), independent of timing — NS-305).
func chaosConfig(callback mockrail.CallbackSender) mockrail.Config {
	return mockrail.Config{
		Seed:        42,
		PTimeout:    0.25, // SendToRail loses the answer -> IN_DOUBT -> TSQ
		PDecline:    0.30, // rail refuses -> reversal
		PDuplicate:  0.50, // successful transfers also get duplicate callbacks
		PTSQTimeout: 0.30, // some TSQs time out -> MANUAL_REVIEW (held in suspense)
		Callback:    callback,
	}
}

// TestChaos_ZeroStrandedDebits is AC-1.
func TestChaos_ZeroStrandedDebits(t *testing.T) {
	pool := testsupport.NewPool(t) // skips when Docker is unavailable
	ctx := context.Background()

	const n = 60
	const amountMinor = 5000

	ledgerClient := dialLedger(t, pool)
	store := transfer.NewPostgresStore(pool)

	// The duplicate-callback seam dials back into the switch in production; here it
	// invokes the driver directly. Late-bound because the driver depends on the
	// rail client, which depends on the rail server, which holds this callback.
	cb := &directCallback{}
	railSrv := mockrail.NewServerWithConfig(chaosConfig(cb))
	railClient := transfer.NewRailClient(mockrailv1.NewRailServiceClient(dialRail(t, railSrv)))

	// Fast TSQ so MANUAL_REVIEW transfers resolve quickly during the drain.
	driver := transfer.NewDriver(store, transfer.NewLedgerClient(ledgerClient), railClient,
		transfer.WithTSQPolicy(3, time.Millisecond))
	cb.driver = driver
	o := transfer.NewOrchestrator(store, driver)

	// Predictor: a no-side-effect copy of the rail with the SAME config. Calling
	// its exported SendToRail/QueryStatus tells us each reference's true outcome,
	// so we can assert the transfer reached exactly that terminal state — proving
	// the split is seed-determined and the recovery resolved every transfer
	// correctly.
	predictor := mockrail.NewServerWithConfig(chaosConfig(nil))

	// --- fire N transfers: each posts its debit synchronously, then sits at
	// DEBITED with a queued transfer.debited event (settlement still pending).
	refs := make([]string, n)
	for i := 0; i < n; i++ {
		ref := refName(i)
		refs[i] = ref
		view, err := o.Create(ctx, ref, transfer.CreateRequest{
			Source: srcAccount, Destination: dstAccount,
			Amount: money.FromMinor(amountMinor), Currency: "NGN", Reference: ref,
		})
		if err != nil {
			t.Fatalf("create %s: %v", ref, err)
		}
		if view.State != transfer.StateDebited {
			t.Fatalf("%s post-create state = %s, want DEBITED", ref, view.State)
		}
	}

	// --- CRASH: switchd dies mid-flow. Every transfer is debited; its in-flight
	// outbox event is lost. Nothing can drive these forward without recovery.
	if _, err := pool.Exec(ctx, `DELETE FROM outbox WHERE published_at IS NULL`); err != nil {
		t.Fatalf("simulate crash (delete outbox): %v", err)
	}
	if got := countNonTerminal(ctx, t, pool); got != n {
		t.Fatalf("after crash: %d non-terminal transfers, want all %d stranded at DEBITED", got, n)
	}

	// --- RESTART: the boot recovery sweep re-enqueues the stranded transfers and
	// the poller drains them to terminal. Loop (bounded) until nothing is left
	// non-terminal, so a multi-step chain (debited -> in_doubt -> reversed) fully
	// resolves.
	deadline := time.Now().Add(60 * time.Second)
	for {
		reenq, err := transfer.NewRecoverer(store).Recover(ctx)
		if err != nil {
			t.Fatalf("recover: %v", err)
		}
		if err := outbox.NewPoller(store.Queries(), driver, outbox.Config{Batch: 16}).Drain(ctx); err != nil {
			t.Fatalf("drain: %v", err)
		}
		if countNonTerminal(ctx, t, pool) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("transfers still non-terminal after recovery deadline (%d left)", countNonTerminal(ctx, t, pool))
		}
		if reenq == 0 {
			time.Sleep(20 * time.Millisecond) // let any future-scheduled retry come due
		}
	}

	// --- wait for in-flight duplicate callbacks to quiesce, so no settlement leg
	// posts during the balance assertions. Callbacks are idempotent no-ops once a
	// transfer is terminal, but we wait so -race sees no late DB activity.
	waitQuiet(t, &cb.inFlight)

	// --- ZERO STRANDED DEBITS: nothing left mid-flow.
	if got := countNonTerminal(ctx, t, pool); got != 0 {
		t.Fatalf("zero-stranded invariant violated: %d transfers still non-terminal", got)
	}

	// --- every transfer reached its true, seed-determined terminal state; tally
	// the split and the expected ledger position from the predictor.
	var settled, reversed, manual int
	var wantSrc, wantDst, wantSuspense int64
	for _, ref := range refs {
		want := predictOutcome(t, predictor, ref)
		got := transferState(ctx, t, pool, ref)
		if got != want {
			t.Errorf("%s terminal state = %s, want %s (seed-determined)", ref, got, want)
		}
		switch want {
		case transfer.StateSettled:
			settled++
			wantSrc += amountMinor // debited, not restored
			wantDst -= amountMinor // credited (asset balance goes negative)
		case transfer.StateReversed:
			reversed++ // source restored, destination untouched, suspense drained
		case transfer.StateManualReview:
			manual++
			wantSrc += amountMinor      // debited, awaiting operator
			wantSuspense += amountMinor // money held in SETTLEMENT suspense
		default:
			t.Errorf("%s reached unexpected terminal state %s", ref, want)
		}
	}
	t.Logf("outcome split over %d transfers: settled=%d reversed=%d manual_review=%d", n, settled, reversed, manual)

	// The chaos must actually exercise both the settle and reversal paths, else
	// the test proves nothing about stranded debits.
	if settled == 0 || reversed == 0 {
		t.Fatalf("chaos did not exercise a mix: settled=%d reversed=%d (need both > 0)", settled, reversed)
	}

	// --- conservation: balances reconcile exactly. Every debit is matched by a
	// credit (settled), restored (reversed), or held in suspense (manual_review).
	// No money is unaccounted for anywhere.
	assertBalance(ctx, t, ledgerClient, srcAccount, wantSrc)
	assertBalance(ctx, t, ledgerClient, dstAccount, wantDst)
	assertBalance(ctx, t, ledgerClient, suspenseAcct, wantSuspense)
}

// --- helpers ---------------------------------------------------------------

// refName is fixed (no timestamp): with a fresh test database each run, the same
// seed + the same references give the same outcome split every run — the
// reproducibility NS-305 guarantees. predictOutcome below verifies, per transfer,
// that the terminal state is exactly the seed-derived outcome.
func refName(i int) string { return "chaos-" + itoa(i) }

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [12]byte
	p := len(b)
	for i > 0 {
		p--
		b[p] = byte('0' + i%10)
		i /= 10
	}
	return string(b[p:])
}

// directCallback is the in-process equivalent of SWITCH_CALLBACK_TARGET: it feeds
// the rail's duplicate success callbacks straight into the driver's idempotent
// intake. inFlight tracks running callbacks so the test can wait for quiescence.
type directCallback struct {
	driver   *transfer.Driver
	inFlight int64
}

func (c *directCallback) SendCallback(reference string, declined bool) {
	atomic.AddInt64(&c.inFlight, 1)
	defer atomic.AddInt64(&c.inFlight, -1)
	verdict := transfer.VerdictSuccess
	if declined {
		verdict = transfer.VerdictDeclined
	}
	// Idempotent: a no-op once the transfer is already terminal.
	_, _ = c.driver.HandleRailCallback(context.Background(), reference, verdict)
}

// waitQuiet blocks until no callback has been in flight across several stable
// checks (covering the gap between the rail's `go` and the callback starting),
// bounded so a stuck callback can't hang the test.
func waitQuiet(t *testing.T, inFlight *int64) {
	t.Helper()
	stable := 0
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt64(inFlight) == 0 {
			if stable++; stable >= 5 {
				return
			}
		} else {
			stable = 0
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("duplicate callbacks did not quiesce (inFlight=%d)", atomic.LoadInt64(inFlight))
}

// predictOutcome returns the terminal state a reference must reach, derived from
// the rail's true (seed-determined) outcome — the same logic the driver follows.
func predictOutcome(t *testing.T, pred *mockrail.Server, ref string) transfer.State {
	t.Helper()
	ctx := context.Background()
	sendResp, sendErr := pred.SendToRail(ctx, &mockrailv1.SendToRailRequest{Reference: ref})
	if sendErr == nil {
		// definitive send: success unless the rail declined.
		if sendResp.GetStatus() == mockrailv1.RailStatus_RAIL_STATUS_DECLINED {
			return transfer.StateReversed
		}
		return transfer.StateSettled
	}
	// sendErr is DeadlineExceeded (timeout) -> in-doubt -> TSQ decides.
	resp, tsqErr := pred.QueryStatus(ctx, &mockrailv1.QueryStatusRequest{Reference: ref})
	if tsqErr != nil {
		return transfer.StateManualReview // TSQ inconclusive -> held in suspense
	}
	if resp.GetStatus() == mockrailv1.RailStatus_RAIL_STATUS_DECLINED {
		return transfer.StateReversed
	}
	return transfer.StateSettled
}

func countNonTerminal(ctx context.Context, t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var c int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM transactions
		 WHERE type='transfer' AND status IN ('pending','debited','in_doubt','reversal_pending')`).
		Scan(&c); err != nil {
		t.Fatalf("count non-terminal: %v", err)
	}
	return c
}

func transferState(ctx context.Context, t *testing.T, pool *pgxpool.Pool, ref string) transfer.State {
	t.Helper()
	var status string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM transactions WHERE reference=$1 AND metadata ? 'source'`, ref).
		Scan(&status); err != nil {
		t.Fatalf("get status %s: %v", ref, err)
	}
	switch status {
	case "settled":
		return transfer.StateSettled
	case "reversed":
		return transfer.StateReversed
	case "manual_review":
		return transfer.StateManualReview
	case "failed":
		return transfer.StateFailed
	default:
		return transfer.State(status)
	}
}

func assertBalance(ctx context.Context, t *testing.T, c ledgerv1.LedgerServiceClient, code string, want int64) {
	t.Helper()
	bal, err := c.GetBalance(ctx, &ledgerv1.GetBalanceRequest{AccountCode: code})
	if err != nil {
		t.Fatalf("GetBalance %s: %v", code, err)
	}
	if bal.GetBalanceMinor() != want {
		t.Errorf("%s balance = %d, want %d", code, bal.GetBalanceMinor(), want)
	}
}

// dialLedger stands up the real ledger gRPC server over bufconn against the pool
// and seeds the two customer accounts (SETTLEMENT is seeded by the migration).
func dialLedger(t *testing.T, pool *pgxpool.Pool) ledgerv1.LedgerServiceClient {
	t.Helper()
	repo := ledgerpg.NewRepository(pool)
	svc := ledger.NewService(repo)
	ctx := context.Background()
	for _, code := range []string{srcAccount, dstAccount} {
		if _, err := repo.Queries().CreateAccount(ctx, ledgerdb.CreateAccountParams{
			Code: code, Name: code, Type: "asset", Currency: "NGN",
		}); err != nil {
			t.Fatalf("seed account %s: %v", code, err)
		}
	}
	conn := serveBufconn(t, func(s *grpc.Server) { ledgerv1.RegisterLedgerServiceServer(s, ledger.NewGRPCServer(svc)) })
	return ledgerv1.NewLedgerServiceClient(conn)
}

// dialRail serves the given rail simulator over bufconn and returns a client conn.
func dialRail(t *testing.T, srv *mockrail.Server) *grpc.ClientConn {
	t.Helper()
	return serveBufconn(t, func(s *grpc.Server) { mockrailv1.RegisterRailServiceServer(s, srv) })
}

func serveBufconn(t *testing.T, register func(*grpc.Server)) *grpc.ClientConn {
	t.Helper()
	lis := bufconn.Listen(1024 * 1024)
	s := grpc.NewServer()
	register(s)
	go func() { _ = s.Serve(lis) }()
	t.Cleanup(s.Stop)
	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial bufconn: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}
