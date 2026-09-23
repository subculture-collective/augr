// Package memory holds the Injector and Reflector prototypes for cross-run
// strategy memory. Nothing in cmd/ or internal/agent constructs them: the
// package is not wired into the pipeline and its behavior is not exercised in
// production. Keep it until an ADR decides whether to integrate or remove it.
package memory
