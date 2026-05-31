package ledger

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	ledgerv1 "github.com/Philip-Nwabuwa/Invariant-Core/api/gen/ledger/v1"
	"github.com/Philip-Nwabuwa/Invariant-Core/internal/ledger/postgres/ledgerdb"
	"github.com/Philip-Nwabuwa/Invariant-Core/pkg/canonical"
	"github.com/Philip-Nwabuwa/Invariant-Core/pkg/money"
)

// GRPCServer adapts the ledger Service to the generated gRPC surface, mapping
// proto messages to and from the domain.
type GRPCServer struct {
	ledgerv1.UnimplementedLedgerServiceServer
	svc *Service
}

// NewGRPCServer wraps a Service for gRPC serving.
func NewGRPCServer(svc *Service) *GRPCServer {
	return &GRPCServer{svc: svc}
}

// Ping reports liveness.
func (g *GRPCServer) Ping(context.Context, *ledgerv1.PingRequest) (*ledgerv1.PingResponse, error) {
	return &ledgerv1.PingResponse{Ok: true}, nil
}

// PostTransaction validates and records a balanced transaction.
func (g *GRPCServer) PostTransaction(ctx context.Context, req *ledgerv1.PostTransactionRequest) (*ledgerv1.PostTransactionResponse, error) {
	entries := make([]EntryInput, 0, len(req.GetEntries()))
	for _, e := range req.GetEntries() {
		entries = append(entries, EntryInput{
			AccountCode: e.GetAccountCode(),
			Direction:   directionFromProto(e.GetDirection()),
			Amount:      money.FromMinor(e.GetAmountMinor()),
			Currency:    e.GetCurrency(),
		})
	}

	txID, err := g.svc.PostTransaction(ctx, PostRequest{
		Reference: req.GetReference(),
		Type:      canonical.Type(req.GetType()),
		Status:    canonical.Status(req.GetStatus()),
		Entries:   entries,
		Metadata:  req.GetMetadata(),
	})
	if err != nil {
		return nil, postErrToStatus(err)
	}
	return &ledgerv1.PostTransactionResponse{TransactionId: txID.String()}, nil
}

// GetBalance returns an account's journal-derived balance.
func (g *GRPCServer) GetBalance(ctx context.Context, req *ledgerv1.GetBalanceRequest) (*ledgerv1.GetBalanceResponse, error) {
	acct, err := g.svc.GetAccount(ctx, req.GetAccountCode())
	if err != nil {
		return nil, lookupErrToStatus(err)
	}
	bal, err := g.svc.GetBalance(ctx, req.GetAccountCode())
	if err != nil {
		return nil, lookupErrToStatus(err)
	}
	return &ledgerv1.GetBalanceResponse{
		AccountCode:  acct.Code,
		BalanceMinor: bal.Minor(),
		Currency:     acct.Currency,
	}, nil
}

// GetAccount returns an account by code.
func (g *GRPCServer) GetAccount(ctx context.Context, req *ledgerv1.GetAccountRequest) (*ledgerv1.GetAccountResponse, error) {
	acct, err := g.svc.GetAccount(ctx, req.GetCode())
	if err != nil {
		return nil, lookupErrToStatus(err)
	}
	return &ledgerv1.GetAccountResponse{Account: accountToProto(acct)}, nil
}

// ListEntries returns a transaction's journal lines.
func (g *GRPCServer) ListEntries(ctx context.Context, req *ledgerv1.ListEntriesRequest) (*ledgerv1.ListEntriesResponse, error) {
	txID, err := uuid.Parse(req.GetTransactionId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid transaction_id: %v", err)
	}
	entries, err := g.svc.ListEntries(ctx, txID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list entries: %v", err)
	}
	out := make([]*ledgerv1.Entry, 0, len(entries))
	for _, e := range entries {
		out = append(out, entryToProto(e))
	}
	return &ledgerv1.ListEntriesResponse{Entries: out}, nil
}

// ExportTransactions streams canonical records over the requested window.
func (g *GRPCServer) ExportTransactions(req *ledgerv1.ExportTransactionsRequest, stream grpc.ServerStreamingServer[ledgerv1.ExportTransactionsResponse]) error {
	from := time.Time{}
	if req.GetFrom() != nil {
		from = req.GetFrom().AsTime()
	}
	to := time.Now().UTC()
	if req.GetTo() != nil {
		to = req.GetTo().AsTime()
	}

	records, err := g.svc.ExportTransactions(stream.Context(), from, to)
	if err != nil {
		return status.Errorf(codes.Internal, "export transactions: %v", err)
	}
	for i := range records {
		if err := stream.Send(&ledgerv1.ExportTransactionsResponse{
			Record: canonicalToProto(records[i]),
		}); err != nil {
			return err
		}
	}
	return nil
}

// --- mapping helpers ---

func directionFromProto(d ledgerv1.Direction) Direction {
	switch d {
	case ledgerv1.Direction_DIRECTION_DEBIT:
		return Debit
	case ledgerv1.Direction_DIRECTION_CREDIT:
		return Credit
	default:
		return Direction("")
	}
}

func directionToProto(d string) ledgerv1.Direction {
	switch Direction(d) {
	case Debit:
		return ledgerv1.Direction_DIRECTION_DEBIT
	case Credit:
		return ledgerv1.Direction_DIRECTION_CREDIT
	default:
		return ledgerv1.Direction_DIRECTION_UNSPECIFIED
	}
}

func accountToProto(a ledgerdb.Account) *ledgerv1.Account {
	return &ledgerv1.Account{
		Id:        a.ID.String(),
		Code:      a.Code,
		Name:      a.Name,
		Type:      a.Type,
		Currency:  a.Currency,
		CreatedAt: timestamppb.New(a.CreatedAt),
	}
}

func entryToProto(e ledgerdb.Entry) *ledgerv1.Entry {
	return &ledgerv1.Entry{
		Id:            e.ID.String(),
		TransactionId: e.TransactionID.String(),
		AccountId:     e.AccountID.String(),
		Direction:     directionToProto(e.Direction),
		AmountMinor:   e.AmountMinor,
		Currency:      e.Currency,
		CreatedAt:     timestamppb.New(e.CreatedAt),
	}
}

func canonicalToProto(r canonical.Record) *ledgerv1.CanonicalRecord {
	rec := &ledgerv1.CanonicalRecord{
		TransactionId: r.TransactionID,
		Reference:     r.Reference,
		Source:        r.Source,
		Destination:   r.Destination,
		AmountMinor:   r.AmountMinor.Minor(),
		Currency:      r.Currency,
		Type:          string(r.Type),
		Status:        string(r.Status),
		InitiatedAt:   timestamppb.New(r.InitiatedAt),
		Metadata:      r.Metadata,
	}
	if r.SettledAt != nil {
		rec.SettledAt = timestamppb.New(*r.SettledAt)
	}
	return rec
}

// postErrToStatus maps PostTransaction domain errors to gRPC status codes.
func postErrToStatus(err error) error {
	switch {
	case errors.Is(err, ErrUnknownAccount):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, ErrTooFewEntries),
		errors.Is(err, ErrUnbalanced),
		errors.Is(err, ErrMixedCurrency),
		errors.Is(err, ErrNonPositiveAmount):
		return status.Error(codes.InvalidArgument, err.Error())
	default:
		return status.Error(codes.Internal, err.Error())
	}
}

func lookupErrToStatus(err error) error {
	if errors.Is(err, ErrUnknownAccount) {
		return status.Error(codes.NotFound, err.Error())
	}
	return status.Error(codes.Internal, err.Error())
}
