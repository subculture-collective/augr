package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/runcontrol"
	"github.com/google/uuid"
)

func TestRunContextRegistryCompositeKeyAndDuplicate(t *testing.T) {
	registry := NewRunContextRegistry()
	id := uuid.New()
	tradeDate := time.Date(2026, 8, 24, 17, 0, 0, 0, time.FixedZone("offset", 3600))
	ctx, cancel := context.WithCancelCause(context.Background())
	if err := registry.Register(id, tradeDate, cancel); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(id, tradeDate.UTC(), cancel); !errors.Is(err, ErrRunAlreadyRegistered) {
		t.Fatalf("duplicate error = %v", err)
	}
	if !registry.Cancel(id, tradeDate.UTC(), runcontrol.Operator) {
		t.Fatal("cancel returned false")
	}
	if !errors.Is(context.Cause(ctx), runcontrol.Operator) {
		t.Fatalf("cause = %v", context.Cause(ctx))
	}
}
