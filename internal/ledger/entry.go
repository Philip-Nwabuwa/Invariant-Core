package ledger

import "github.com/Philip-Nwabuwa/Invariant-Core/pkg/money"

// Direction is the side of the ledger an entry posts to. The stored amount is
// always a positive magnitude; the direction carries the sign (db/schema.sql).
type Direction string

// Entry directions, mirroring the entries.direction CHECK constraint.
const (
	Debit  Direction = "debit"
	Credit Direction = "credit"
)

// EntryInput is one line of a posting request: which account, which side, and a
// positive magnitude. All entries in a request must share a currency.
type EntryInput struct {
	AccountCode string
	Direction   Direction
	Amount      money.Amount
	Currency    string
}
