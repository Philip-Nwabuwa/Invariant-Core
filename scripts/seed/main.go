// Command seed creates the demo customer accounts used by the local Sprint 2
// walkthrough. The system accounts (SETTLEMENT, FEES) are already seeded by
// migration 0001; this adds the customer accounts a transfer moves money
// between. It is idempotent: re-running skips accounts that already exist.
package main

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Philip-Nwabuwa/Invariant-Core/internal/ledger/postgres"
	"github.com/Philip-Nwabuwa/Invariant-Core/internal/ledger/postgres/ledgerdb"
	"github.com/Philip-Nwabuwa/Invariant-Core/internal/serviceboot"
)

const defaultDBURL = "postgres://invariantcore:invariantcore@localhost:5432/invariantcore?sslmode=disable"

// demoAccounts are the customer accounts a transfer can name as source/destination.
var demoAccounts = []ledgerdb.CreateAccountParams{
	{Code: "CUST-001", Name: "Demo Customer 001", Type: "asset", Currency: "NGN"},
	{Code: "CUST-002", Name: "Demo Customer 002", Type: "asset", Currency: "NGN"},
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := postgres.NewPool(ctx, serviceboot.EnvOr("DB_URL", defaultDBURL))
	if err != nil {
		log.Fatalf("seed: database: %v", err)
	}
	defer pool.Close()

	q := postgres.NewRepository(pool).Queries()
	for _, acct := range demoAccounts {
		if _, err := q.GetAccountByCode(ctx, acct.Code); err == nil {
			log.Printf("seed: %s already exists, skipping", acct.Code)
			continue
		} else if !errors.Is(err, pgx.ErrNoRows) {
			log.Fatalf("seed: lookup %s: %v", acct.Code, err)
		}
		if _, err := q.CreateAccount(ctx, acct); err != nil {
			log.Fatalf("seed: create %s: %v", acct.Code, err)
		}
		log.Printf("seed: created %s (%s)", acct.Code, acct.Type)
	}
	log.Println("seed: done")
}
