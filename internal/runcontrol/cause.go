package runcontrol

import (
	"context"
	"errors"
)

type Cause string

const (
	Operator   Cause = "operator"
	Shutdown   Cause = "shutdown"
	Stale      Cause = "stale"
	KillSwitch Cause = "kill_switch"
)

func (c Cause) Error() string { return "pipeline run cancelled: " + string(c) }

// CauseFromErrorMessage recognizes only cancellation messages emitted by Cause.Error.
func CauseFromErrorMessage(message string) (Cause, bool) {
	for _, cause := range [...]Cause{Operator, Shutdown, Stale, KillSwitch} {
		if message == cause.Error() {
			return cause, true
		}
	}
	return "", false
}

// JoinCauseFromErrorMessage preserves a typed cancellation cause from a durable receipt.
func JoinCauseFromErrorMessage(err error, message string) error {
	cause, ok := CauseFromErrorMessage(message)
	if !ok || errors.Is(err, cause) {
		return err
	}
	return errors.Join(err, cause)
}

func TypedCause(ctx context.Context) (Cause, bool) {
	var cause Cause
	if !errors.As(context.Cause(ctx), &cause) {
		return "", false
	}
	switch cause {
	case Operator, Shutdown, Stale, KillSwitch:
		return cause, true
	default:
		return "", false
	}
}

func IsCancelled(ctx context.Context) bool {
	cause, ok := TypedCause(ctx)
	return ok && cause != Stale
}
