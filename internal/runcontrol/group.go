package runcontrol

import (
	"context"
	"errors"
	"sync"
)

// ErrDraining is returned when work is submitted after shutdown begins.
var ErrDraining = errors.New("runcontrol: group is draining")

type leaseKey struct{}

// Group admits process-local work and drains it during shutdown.
type Group struct {
	mu        sync.Mutex
	accepting bool
	started   bool
	nextID    uint64
	inFlight  int
	cancels   map[uint64]context.CancelCauseFunc
	wg        sync.WaitGroup
}

// Lease owns one admission. Done is safe to call more than once.
type Lease struct {
	once  sync.Once
	group *Group
	id    uint64
}

// NewGroup constructs an accepting group.
func NewGroup() *Group {
	return &Group{accepting: true, started: true, cancels: make(map[uint64]context.CancelCauseFunc)}
}

// Admit registers work before it is launched and returns a marked, cancellable context.
func (g *Group) Admit(parent context.Context) (context.Context, *Lease, error) {
	if parent == nil {
		parent = context.Background()
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.started {
		g.started = true
		g.accepting = true
		g.cancels = make(map[uint64]context.CancelCauseFunc)
	}
	if !g.accepting {
		return nil, nil, ErrDraining
	}
	g.nextID++
	id := g.nextID
	ctx, cancel := context.WithCancelCause(parent)
	ctx = context.WithValue(ctx, leaseKey{}, g)
	g.cancels[id] = cancel
	g.inFlight++
	g.wg.Add(1)
	return ctx, &Lease{group: g, id: id}, nil
}

// HasLease reports whether ctx already owns an admission from this group.
func (g *Group) HasLease(ctx context.Context) bool {
	return ctx != nil && ctx.Value(leaseKey{}) == g
}

// Go admits fn before starting its goroutine.
func (g *Group) Go(parent context.Context, fn func(context.Context)) error {
	ctx, lease, err := g.Admit(parent)
	if err != nil {
		return err
	}
	go func() {
		defer lease.Done()
		fn(ctx)
	}()
	return nil
}

// Done releases the admission once.
func (l *Lease) Done() {
	if l == nil || l.group == nil {
		return
	}
	l.once.Do(func() {
		g := l.group
		g.mu.Lock()
		delete(g.cancels, l.id)
		g.inFlight--
		g.mu.Unlock()
		g.wg.Done()
	})
}

// Stop rejects new work and cancels every admitted context with cause.
func (g *Group) Stop(cause error) {
	if cause == nil {
		cause = Shutdown
	}
	g.mu.Lock()
	if !g.started {
		g.started = true
		g.cancels = make(map[uint64]context.CancelCauseFunc)
	}
	g.accepting = false
	for _, cancel := range g.cancels {
		cancel(cause)
	}
	g.mu.Unlock()
}

// Wait closes admission before waiting, preventing concurrent WaitGroup.Add calls.
func (g *Group) Wait() {
	g.Stop(Shutdown)
	g.wg.Wait()
}

// StopAndWait stops admission, cancels work, and joins all admitted work.
func (g *Group) StopAndWait(cause error) {
	g.Stop(cause)
	g.wg.Wait()
}

// InFlight returns the number of admitted leases not yet released.
func (g *Group) InFlight() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.inFlight
}
