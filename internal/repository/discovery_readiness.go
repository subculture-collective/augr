package repository

// ImmutableBindingLock means deployment is authoritatively blocked because an
// execution data loader is not bound to immutable evidence.
type ImmutableBindingLock struct {
	reason string
}

// NewImmutableBindingLock creates a lock with an immutable operator-facing reason.
func NewImmutableBindingLock(reason string) ImmutableBindingLock {
	return ImmutableBindingLock{reason: reason}
}

func (e ImmutableBindingLock) Error() string {
	return e.reason
}

// Reason returns the stable operator-facing lock reason.
func (e ImmutableBindingLock) Reason() string {
	return e.reason
}

// Is matches immutable-binding locks by their stable reason.
func (e ImmutableBindingLock) Is(target error) bool {
	other, ok := target.(ImmutableBindingLock)
	return ok && e.reason == other.reason
}
