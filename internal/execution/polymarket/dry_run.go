package polymarket

import (
	"context"
	"errors"
	"net/url"
	"strings"
)

const dryRunParamName = "dry"

func withDryRunQuery(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return rawURL + "?" + dryRunParamName + "=1"
	}
	query := parsed.Query()
	query.Set(dryRunParamName, "1")
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

// ClassifyDryRunError categorizes rejected dry-run responses.
func ClassifyDryRunError(err error) (string, bool) {
	if err == nil {
		return "", false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout", true
	}
	lower := strings.ToLower(err.Error())
	switch {
	case strings.Contains(lower, "ghost_fill"):
		return "ghost_fill", true
	case strings.Contains(lower, "insufficient") || strings.Contains(lower, "nsf") || strings.Contains(lower, "balance"):
		return "nsf", true
	case strings.Contains(lower, "timeout"):
		return "timeout", true
	case strings.Contains(lower, "reject") || strings.Contains(lower, "rejected") || strings.Contains(lower, "error"):
		return "other", true
	default:
		return "other", true
	}
}
