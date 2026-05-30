// Package money represents monetary values as integer minor units (kobo).
//
// ADR-0001: floating-point money is prohibited. Every amount is an int64 count
// of minor units; all arithmetic is explicit and exact. Converting to a
// human-readable major-unit string is a display-boundary concern handled by
// String, never by storing a float.
package money

import (
	"fmt"
	"strconv"
)

// Amount is a signed quantity of minor units (e.g. kobo, where 100 kobo = ₦1).
// Direction/sign is carried by the value itself.
type Amount int64

// Zero is the additive identity.
const Zero Amount = 0

// minorPerMajor is the number of minor units in one major unit. NGN-only v1.
const minorPerMajor = 100

// FromMinor constructs an Amount from a raw count of minor units.
func FromMinor(m int64) Amount { return Amount(m) }

// Minor returns the raw count of minor units.
func (a Amount) Minor() int64 { return int64(a) }

// Add returns a + b.
func (a Amount) Add(b Amount) Amount { return a + b }

// Sub returns a - b.
func (a Amount) Sub(b Amount) Amount { return a - b }

// Neg returns -a. Used to build compensating (reversal) entries.
func (a Amount) Neg() Amount { return -a }

// IsZero reports whether the amount is exactly zero.
func (a Amount) IsZero() bool { return a == 0 }

// String renders the amount in major units with two decimal places, e.g.
// FromMinor(123456) -> "1234.56", FromMinor(-5) -> "-0.05". This is the display
// boundary; the stored representation stays integer minor units.
func (a Amount) String() string {
	v := int64(a)
	neg := v < 0
	if neg {
		v = -v
	}
	s := fmt.Sprintf("%d.%02d", v/minorPerMajor, v%minorPerMajor)
	if neg {
		s = "-" + s
	}
	return s
}

// MarshalJSON encodes the amount as a bare integer count of minor units, so the
// canonical record's amount_minor is always an integer in JSON — never a float.
func (a Amount) MarshalJSON() ([]byte, error) {
	return []byte(strconv.FormatInt(int64(a), 10)), nil
}

// UnmarshalJSON decodes an integer count of minor units. A fractional or
// non-numeric value is rejected rather than silently truncated.
func (a *Amount) UnmarshalJSON(b []byte) error {
	v, err := strconv.ParseInt(string(b), 10, 64)
	if err != nil {
		return fmt.Errorf("money: amount must be an integer of minor units: %w", err)
	}
	*a = Amount(v)
	return nil
}
