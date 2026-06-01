package adapters

import (
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/Philip-Nwabuwa/Invariant-Core/pkg/canonical"
)

// statusToNIBSS is the inverse of the settled/failed status tokens, used when
// emitting a NIBSS settlement file. It keeps the written format consistent with
// what NewNIBSSReader parses.
var statusToNIBSS = map[canonical.Status]string{
	canonical.StatusSettled:  "00",
	canonical.StatusPending:  "01",
	canonical.StatusFailed:   "09",
	canonical.StatusReversed: "REVERSED",
	canonical.StatusTimedOut: "TIMEOUT",
	canonical.StatusDebited:  "01",
}

// WriteNIBSS emits records as a NIBSS-style settlement CSV (the layout
// NewNIBSSReader parses). It is the counterpart used by the fixture generator.
func WriteNIBSS(w io.Writer, recs []canonical.Record) error {
	cw := csv.NewWriter(w)
	if err := cw.Write(nibssColumns); err != nil {
		return fmt.Errorf("write nibss header: %w", err)
	}
	for _, r := range recs {
		token, ok := statusToNIBSS[r.Status]
		if !ok {
			token = "00"
		}
		row := []string{
			r.Reference,
			r.Source,
			r.Destination,
			strconv.FormatInt(r.AmountMinor.Minor(), 10),
			r.Currency,
			token,
			r.InitiatedAt.UTC().Format(time.RFC3339),
		}
		if err := cw.Write(row); err != nil {
			return fmt.Errorf("write nibss row: %w", err)
		}
	}
	cw.Flush()
	return cw.Error()
}
