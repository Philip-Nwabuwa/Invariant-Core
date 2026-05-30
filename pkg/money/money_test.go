package money

import (
	"encoding/json"
	"testing"
)

func TestArithmetic(t *testing.T) {
	tests := []struct {
		name     string
		a, b     Amount
		add, sub Amount
		negA     Amount
		aIsZero  bool
	}{
		{"positives", FromMinor(150), FromMinor(50), FromMinor(200), FromMinor(100), FromMinor(-150), false},
		{"crossing zero", FromMinor(50), FromMinor(150), FromMinor(200), FromMinor(-100), FromMinor(-50), false},
		{"zero left", Zero, FromMinor(99), FromMinor(99), FromMinor(-99), Zero, true},
		{"both negative", FromMinor(-10), FromMinor(-5), FromMinor(-15), FromMinor(-5), FromMinor(10), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.a.Add(tt.b); got != tt.add {
				t.Errorf("Add: got %d, want %d", got.Minor(), tt.add.Minor())
			}
			if got := tt.a.Sub(tt.b); got != tt.sub {
				t.Errorf("Sub: got %d, want %d", got.Minor(), tt.sub.Minor())
			}
			if got := tt.a.Neg(); got != tt.negA {
				t.Errorf("Neg: got %d, want %d", got.Minor(), tt.negA.Minor())
			}
			if got := tt.a.IsZero(); got != tt.aIsZero {
				t.Errorf("IsZero: got %v, want %v", got, tt.aIsZero)
			}
		})
	}
}

func TestString(t *testing.T) {
	tests := []struct {
		minor int64
		want  string
	}{
		{0, "0.00"},
		{5, "0.05"},
		{99, "0.99"},
		{100, "1.00"},
		{123456, "1234.56"},
		{-5, "-0.05"},
		{-123456, "-1234.56"},
	}
	for _, tt := range tests {
		if got := FromMinor(tt.minor).String(); got != tt.want {
			t.Errorf("FromMinor(%d).String() = %q, want %q", tt.minor, got, tt.want)
		}
	}
}

func TestJSONRoundTrip(t *testing.T) {
	for _, minor := range []int64{0, 1, -1, 123456, -987654321} {
		a := FromMinor(minor)
		b, err := json.Marshal(a)
		if err != nil {
			t.Fatalf("Marshal(%d): %v", minor, err)
		}
		// Must serialize as a bare integer, not a float or string.
		if string(b) != itoa(minor) {
			t.Errorf("Marshal(%d) = %s, want %s", minor, b, itoa(minor))
		}
		var got Amount
		if err := json.Unmarshal(b, &got); err != nil {
			t.Fatalf("Unmarshal(%s): %v", b, err)
		}
		if got != a {
			t.Errorf("round-trip: got %d, want %d", got.Minor(), minor)
		}
	}
}

func TestUnmarshalRejectsFloat(t *testing.T) {
	var a Amount
	if err := json.Unmarshal([]byte("12.34"), &a); err == nil {
		t.Fatal("expected error unmarshalling a float into Amount, got nil")
	}
}

// itoa avoids importing strconv twice; small helper for the expected encoding.
func itoa(v int64) string {
	b, _ := FromMinor(v).MarshalJSON()
	return string(b)
}
