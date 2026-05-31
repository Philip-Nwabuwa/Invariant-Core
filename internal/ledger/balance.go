package ledger

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/Philip-Nwabuwa/Invariant-Core/pkg/money"
)

// GetBalance derives an account's balance directly from its journal entries
// (the source of truth), applying the account's normal-balance direction. The
// cached account_balances row is an optimization maintained by PostTransaction;
// this read never depends on it.
func (s *Service) GetBalance(ctx context.Context, accountCode string) (money.Amount, error) {
	q := s.repo.Queries()
	acct, err := q.GetAccountByCode(ctx, accountCode)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return money.Zero, fmt.Errorf("%w: %s", ErrUnknownAccount, accountCode)
		}
		return money.Zero, fmt.Errorf("get account %s: %w", accountCode, err)
	}
	sum, err := q.SumEntriesByAccount(ctx, acct.ID)
	if err != nil {
		return money.Zero, fmt.Errorf("sum entries for %s: %w", accountCode, err)
	}
	return DeriveBalance(AccountType(acct.Type), sum.DebitMinor, sum.CreditMinor), nil
}
