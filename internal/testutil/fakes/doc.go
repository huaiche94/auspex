// Package fakes provides hand-written test doubles for the frozen
// cross-component service interfaces in internal/app/ports.go
// (agents/runtime.md Part B exclusive paths, "coordinate with the qa
// role"). runtime-b02 is the first populator of this directory: the app
// wiring layer (internal/app/wiring) must be constructible and testable
// before any real service implementation exists (EXECUTION_DAG.md
// runtime-b02: "can start against claude-provider/checkpoint/predictor
// fakes"), and qa's later integration tests are expected to reuse these
// doubles rather than each role hand-rolling its own.
//
// Every fake follows one pattern:
//
//   - one exported struct per frozen service interface, named
//     Fake<InterfaceName>, with one optional function field per method
//     (<Method>Func) — a test configures exactly the methods it needs;
//   - a compile-time `var _ app.X = (*FakeX)(nil)` assertion, so a frozen
//     contract change breaks this package at build time, not at a
//     downstream role's test time;
//   - calling a method whose Func field is nil fails loud with the frozen
//     domain.Error shape (ErrCodeUnavailable, Retryable: false, Details
//     naming the fake and method) instead of silently returning zero
//     values — a mis-wired test should read as "fake not configured," not
//     as a mysterious empty result (mirrors the unknown-is-not-zero
//     discipline in CONTRACT_FREEZE.md).
//
// The fakes deliberately do not record calls, count invocations, or
// synchronize internal state: a test that needs those behaviors can build
// them inside its own Func closures (which also keeps the fakes trivially
// race-safe — configure fields before use, never mutate them
// concurrently). Adding shared recording machinery before a test needs it
// would violate Constitution §7 rule 10 (no abstractions a later
// milestone would need but the current one doesn't).
package fakes
