package domain

import (
	"fmt"
	"strings"
	"time"
)

// ParseStrictOCC validates a canonical unprefixed OCC identity without calendar
// normalization or signed numeric fields. Legacy ParseOCC behavior is unchanged.
// Returned reference defaults are derived, not provider-reported contract terms.
func ParseStrictOCC(symbol string) (*OptionContract, error) {
	if len(symbol) < 16 || len(symbol) > 21 {
		return nil, fmt.Errorf("occ: canonical symbol length required")
	}
	root := symbol[:len(symbol)-15]
	date := symbol[len(symbol)-15 : len(symbol)-9]
	strike := symbol[len(symbol)-8:]
	if strings.Trim(root, "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789") != "" || strings.Trim(date+strike, "0123456789") != "" {
		return nil, fmt.Errorf("occ: canonical root and unsigned digits required")
	}
	if _, err := time.Parse("20060102", "20"+date); err != nil {
		return nil, fmt.Errorf("occ: invalid canonical calendar date: %w", err)
	}
	return ParseOCC(symbol)
}
