package domain

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPipelineRunRefCompleteIdentity(t *testing.T) {
	tradeDate := time.Date(2026, time.August, 27, 0, 0, 0, 0, time.UTC)
	ref := PipelineRunRef{ID: uuid.MustParse("10000000-0000-4000-8000-000000000001"), TradeDate: tradeDate}

	if ref.ID == uuid.Nil || !ref.TradeDate.Equal(tradeDate) {
		t.Fatalf("PipelineRunRef = %+v, want complete run identity", ref)
	}
}
