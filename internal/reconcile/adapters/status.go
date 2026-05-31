package adapters

import (
	"strings"

	"github.com/Philip-Nwabuwa/Invariant-Core/pkg/canonical"
)

// statusTokens maps the assorted status spellings seen in settlement files
// (NIBSS numeric response codes plus the usual English words) onto the canonical
// lifecycle statuses. Lookup is case-insensitive (keys are upper-cased).
var statusTokens = map[string]canonical.Status{
	"00":         canonical.StatusSettled,
	"SUCCESS":    canonical.StatusSettled,
	"SUCCESSFUL": canonical.StatusSettled,
	"SETTLED":    canonical.StatusSettled,
	"COMPLETED":  canonical.StatusSettled,
	"01":         canonical.StatusPending,
	"PENDING":    canonical.StatusPending,
	"09":         canonical.StatusFailed,
	"91":         canonical.StatusFailed,
	"FAILED":     canonical.StatusFailed,
	"DECLINED":   canonical.StatusFailed,
	"REVERSED":   canonical.StatusReversed,
	"TIMEOUT":    canonical.StatusTimedOut,
	"TIMED_OUT":  canonical.StatusTimedOut,
}

// parseStatus maps a settlement-file status token to a canonical status. An empty
// token defaults to settled — a row present in a settlement file is settled money
// unless it says otherwise. An unrecognized non-empty token is an error.
func parseStatus(token string) (canonical.Status, error) {
	t := strings.ToUpper(strings.TrimSpace(token))
	if t == "" {
		return canonical.StatusSettled, nil
	}
	if s, ok := statusTokens[t]; ok {
		return s, nil
	}
	return "", ErrUnknownStatus
}
