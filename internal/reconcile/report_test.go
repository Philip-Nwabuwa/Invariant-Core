package reconcile

import (
	"strings"
	"testing"
	"time"

	"github.com/Philip-Nwabuwa/Invariant-Core/pkg/canonical"
)

func sampleResult(t *testing.T) Result {
	t.Helper()
	internal := []canonical.Record{
		transfer("R1", 5000, canonical.StatusSettled),
		transfer("R2", 5000, canonical.StatusSettled),
		transfer("R3", 5000, canonical.StatusFailed),
	}
	external := &sliceStream{recs: []canonical.Record{
		transfer("R1", 5000, canonical.StatusSettled),
		transfer("R2", 6000, canonical.StatusSettled),
		transfer("R9", 100, canonical.StatusSettled),
	}}
	res, err := Match(internal, external, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestReport_Deterministic(t *testing.T) {
	res := sampleResult(t)
	r := NewReport("internal.jsonl", "settlement.csv", res)

	firstJSON, err := r.JSON()
	if err != nil {
		t.Fatal(err)
	}
	firstText := r.Text()
	for i := 0; i < 5; i++ {
		j, err := r.JSON()
		if err != nil {
			t.Fatal(err)
		}
		if string(j) != string(firstJSON) {
			t.Fatalf("JSON not deterministic:\n%s\nvs\n%s", j, firstJSON)
		}
		if r.Text() != firstText {
			t.Fatalf("text not deterministic")
		}
	}
}

func TestReport_TextContent(t *testing.T) {
	res := sampleResult(t)
	text := NewReport("in", "ext", res).Text()
	for _, want := range []string{"matched:    1", "amount_mismatch", "unmatched_external", "pending_reversal"} {
		if !strings.Contains(text, want) {
			t.Errorf("text missing %q:\n%s", want, text)
		}
	}
}
