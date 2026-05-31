package ledger

import "github.com/Philip-Nwabuwa/Invariant-Core/pkg/money"

// AccountType is the chart-of-accounts classification. It determines the
// account's normal-balance direction, which is what makes a derived balance
// meaningful (ARCHITECTURE §3, db/schema.sql).
type AccountType string

// Account types, mirroring the accounts.type CHECK constraint.
const (
	TypeAsset     AccountType = "asset"
	TypeLiability AccountType = "liability"
	TypeEquity    AccountType = "equity"
	TypeRevenue   AccountType = "revenue"
	TypeExpense   AccountType = "expense"
)

// IsDebitNormal reports whether the type increases on a debit. Assets and
// expenses are debit-normal; liabilities, equity, and revenue are credit-normal.
func (t AccountType) IsDebitNormal() bool {
	switch t {
	case TypeAsset, TypeExpense:
		return true
	default:
		return false
	}
}

// DeriveBalance computes an account's balance from its summed debit and credit
// minor units, applying the type's normal-balance direction. Debit-normal:
// debits − credits; credit-normal: credits − debits.
func DeriveBalance(t AccountType, debitMinor, creditMinor int64) money.Amount {
	if t.IsDebitNormal() {
		return money.FromMinor(debitMinor - creditMinor)
	}
	return money.FromMinor(creditMinor - debitMinor)
}
