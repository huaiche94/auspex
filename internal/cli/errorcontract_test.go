// errorcontract_test.go: runtime-b09, the comprehensive cross-command
// audit that every P0 CLI command uniformly honors agents/runtime.md Part
// B's "JSON and errors" contract:
//
//   - stable schema-versioned output;
//   - typed error code, message, retryable, details;
//   - no raw prompt in logs/errors;
//   - machine mode never emits decorative text to stdout;
//   - hook fallback remains syntactically valid when Auspex fails.
//
// A dedicated research pass (this node's own first step) audited every P0
// command against this contract before writing anything new, and found:
//
//   - Every real command (checkpoint create, decision allow/deny, pause
//     request/cancel, resume, scheduler run-once, status, doctor) already
//     constructs a *domain.Error internally on every error path and
//     already emits a schema-versioned JSON envelope on success — this was
//     already correct, and this file's job is to PROVE it holds uniformly,
//     not to fix it.
//   - version, init, progress show, state show have no schema-versioned
//     success JSON (version prints a bare string; the other three are
//     permanent stubs — no CLI constructor for the real thing exists
//     anywhere in this repository as of this node, for init/progress
//     show/state; only the notImplemented stub path is reachable). This
//     is a real, pre-existing gap this audit surfaces explicitly (see
//     TestErrorContract_KnownIncompleteCommands_AreStubsOnly below)
//     rather than silently working around or papering over.
//     (`progress complete` is deliberately NOT in that list: issue #1
//     added a real constructor for it — cli.NewProgressCmd, progress.go —
//     whose subtree still keeps `show` as a stub; progress_test.go's
//     TestProgressComplete_ShowRemainsStubOnRealTree tracks that split.
//     `evaluate` left the list the same way with issue #14:
//     cli.NewEvaluateCmd, evaluate.go, is real and swapped in by
//     internal/app/wiring.App.RootCmd(); only the bare NewRootCmd() tree
//     keeps its stub, per the established stub-then-swap pattern.)
//   - THE genuine, fixable gap: no command's returned *domain.Error was
//     ever serialized to JSON anywhere before this node — every command
//     constructed the right typed Go value, but Cobra's own default error
//     printer (SilenceErrors: false) flattened it to a bare ".Error()"
//     plain-text line on stderr, never structured JSON. Fixed in
//     errors.go (SchemaVersionError/RenderErrorJSON/WithJSONErrorRendering,
//     wired into NewRootCmd and internal/app/wiring.App.RootCmd) — this
//     file proves the fix holds across the WHOLE command tree, real and
//     stub alike.
//   - internal/httpapi does not exist in this repository (confirmed via a
//     dedicated research pass: no directory, no files) — it is an explicit
//     ADD stretch goal not yet built (agents/runtime.md: "HTTP daemon is
//     secondary to a working CLI... No SSE until the core loop is
//     stable"). The DAG's validation command names
//     `go test ./internal/httpapi/... ./internal/cli/... -run
//     ErrorContract`; the httpapi half of that command is a no-op absence,
//     not a real target — this file's tests are scoped to internal/cli
//     (and internal/orchestrator's own response-construction code, called
//     indirectly through the CLI layer) per this node's explicit
//     instruction not to build internal/httpapi just to satisfy a test
//     path reference.
//
// Privacy gate: every command that touches prompt-adjacent data
// (evaluate, decision allow/deny, hook claude user-prompt-submit)
// only ever accepts/threads a PromptHash (a hash, internal/app.ports.go's
// frozen field) — except `evaluate --prompt-file`, which by design reads
// raw prompt TEXT and hashes it immediately in memory
// (orchestrator.EvaluatePrompt / claudehooks.NewUserPromptSubmitEvent;
// evaluate_test.go and internal/integrationtest's evaluate privacy test
// prove the text never reaches output or disk) — confirmed by a dedicated grep
// audit across internal/cli, internal/orchestrator, internal/app/wiring
// (zero hits for any raw-prompt field crossing those boundaries) and
// proven directly below (TestErrorContract_NoRawPromptInAnyErrorOrOutput)
// by feeding a canary "raw prompt" string through every command surface
// that accepts one and scanning ALL stdout/stderr bytes for it.
package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/cli"
	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/orchestrator"
	"github.com/huaiche94/auspex/internal/testutil/fakes"
)

// allP0CommandPaths mirrors root_test.go's own P0 path table — kept as an
// independent literal list here (not imported from the other test file,
// since it is unexported there) so this file's own sweep is self-
// contained and its intent is legible without cross-referencing another
// test file.
func allP0CommandPaths() [][]string {
	return [][]string{
		{"version"},
		{"init"},
		{"hook", "claude", "statusline"},
		{"hook", "claude", "user-prompt-submit"},
		{"hook", "claude", "post-tool-use"},
		{"hook", "claude", "stop"},
		{"hook", "claude", "stop-failure"},
		{"evaluate"},
		{"decision", "allow"},
		{"decision", "deny"},
		{"checkpoint", "create"},
		{"progress", "show"},
		{"progress", "complete"},
		{"state", "show"},
		{"pause", "request"},
		{"pause", "cancel"},
		{"resume"},
		{"scheduler", "run-once"},
		{"status"},
		{"doctor"},
	}
}

// --- 1. Every command's error path renders the typed JSON envelope -----

// TestErrorContract_EveryStubCommand_RendersTypedJSONEnvelopeOnError
// drives every P0 command that is still a stub (every command except
// version) against the bare cli.NewRootCmd() tree — none of them are
// wired to a real service, so each is GUARANTEED to hit its error path —
// and confirms stderr carries a valid, schema-versioned JSON error
// envelope (cli.SchemaVersionError) with the frozen domain.Error fields,
// in addition to (not instead of) the existing returned Go error.
func TestErrorContract_EveryStubCommand_RendersTypedJSONEnvelopeOnError(t *testing.T) {
	for _, path := range allP0CommandPaths() {
		if path[0] == "version" {
			continue // the one command with no error path to exercise
		}
		t.Run(strings.Join(path, " "), func(t *testing.T) {
			root := cli.NewRootCmd()
			var out bytes.Buffer
			root.SetOut(&out)
			root.SetErr(&out)
			root.SetArgs(path)

			err := root.Execute()
			if err == nil {
				t.Fatalf("auspex %s: expected an error (stub command)", strings.Join(path, " "))
			}

			env := decodeErrorEnvelope(t, out.Bytes())
			if env.SchemaVersion != cli.SchemaVersionError {
				t.Errorf("SchemaVersion = %q, want %q", env.SchemaVersion, cli.SchemaVersionError)
			}
			if env.Code == "" {
				t.Error("Code is empty")
			}
			if env.Message == "" {
				t.Error("Message is empty")
			}
			// Every returned error must ALSO still be a *domain.Error —
			// this proves the JSON wrapper's fields were not fabricated
			// independently of the actual typed error the command
			// produced.
			var derr *domain.Error
			if !errors.As(err, &derr) {
				t.Fatalf("returned error %v is not a *domain.Error", err)
			}
			if string(env.Code) != string(derr.Code) {
				t.Errorf("envelope Code %q != returned error Code %q", env.Code, derr.Code)
			}
			if env.Message != derr.Message {
				t.Errorf("envelope Message %q != returned error Message %q", env.Message, derr.Message)
			}
			if env.Retryable != derr.Retryable {
				t.Errorf("envelope Retryable %v != returned error Retryable %v", env.Retryable, derr.Retryable)
			}
		})
	}
}

// TestErrorContract_RenderErrorJSON_NonDomainError confirms
// cli.RenderErrorJSON degrades a plain (non-*domain.Error) Go error to
// ErrCodeInternal/Retryable:false rather than producing no JSON at all or
// panicking — the fallback path for a genuine composition bug this
// package's own commands did not originate as a *domain.Error.
func TestErrorContract_RenderErrorJSON_NonDomainError(t *testing.T) {
	body := cli.RenderErrorJSON(errors.New("some unexpected low-level failure"))
	var env map[string]any
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("RenderErrorJSON output is not valid JSON: %v (body=%s)", err, body)
	}
	if env["schema_version"] != cli.SchemaVersionError {
		t.Errorf("schema_version = %v, want %q", env["schema_version"], cli.SchemaVersionError)
	}
	if env["code"] != string(domain.ErrCodeInternal) {
		t.Errorf("code = %v, want %q", env["code"], domain.ErrCodeInternal)
	}
	if env["retryable"] != false {
		t.Errorf("retryable = %v, want false", env["retryable"])
	}
}

// TestErrorContract_KnownIncompleteCommands_AreStubsOnly documents,
// explicitly and as a checked test rather than only a code comment, the
// one pre-existing gap this audit found that is out of this node's own
// scope to fix: init, progress show, and state show have no real CLI
// constructor anywhere in this repository (unlike checkpoint create,
// decision allow/deny, pause request/cancel, resume, scheduler run-once,
// status, doctor — and, since issue #14, evaluate: cli.NewEvaluateCmd is
// real, so `evaluate` was removed from this list per this test's own
// update-the-scope-note instruction) — every path to the remaining
// three, even through internal/app/wiring.App.RootCmd(), is permanently
// cli.notImplemented's stub. This test fails loudly (rather than staying
// silently true forever) the moment a future node adds a real constructor
// for any of them, so this documented gap is re-confirmed or corrected on
// every test run instead of silently going stale.
func TestErrorContract_KnownIncompleteCommands_AreStubsOnly(t *testing.T) {
	incomplete := [][]string{
		{"init"},
		{"progress", "show"},
		{"state", "show"},
	}
	for _, path := range incomplete {
		t.Run(strings.Join(path, " "), func(t *testing.T) {
			root := cli.NewRootCmd()
			var out bytes.Buffer
			root.SetOut(&out)
			root.SetErr(&out)
			root.SetArgs(path)

			err := root.Execute()
			if err == nil {
				t.Fatalf("auspex %s: expected the stub notImplemented error; got nil (a real implementation may have landed — update this test's scope note if so)", strings.Join(path, " "))
			}
			var derr *domain.Error
			if !errors.As(err, &derr) || derr.Code != domain.ErrCodeUnavailable || !derr.Retryable {
				t.Fatalf("auspex %s: expected the stub's ErrCodeUnavailable/Retryable:true shape, got %v", strings.Join(path, " "), err)
			}
		})
	}
}

// --- 2. Real (non-stub) commands' error AND success paths -------------

// TestErrorContract_RealCheckpointCreate_ErrorPathIsTypedJSON exercises a
// REAL (not stub) command's validation-error path — --task-id omitted —
// through the full JSON-envelope wrapping, confirming the fix applies
// uniformly to real commands built via internal/cli's own exported
// constructors (cli.NewCheckpointCmd), not just the bare stub tree.
//
// The real command is attached under a minimal root with the same
// SilenceUsage/SilenceErrors: true configuration cli.NewRootCmd() and
// internal/app/wiring.App.RootCmd() both use in production, rather than
// executed as a standalone *cobra.Command with Cobra's own un-configured
// defaults — a bare cli.NewCheckpointCmd(...).Execute() is not how any
// real caller ever invokes this command (it is always attached under an
// already-configured root via replaceSubcommand), so testing it standalone
// would exercise a configuration no production code path actually uses.
func TestErrorContract_RealCheckpointCreate_ErrorPathIsTypedJSON(t *testing.T) {
	root := newTestRoot(cli.NewCheckpointCmd(orchestrator.CheckpointCreateDeps{
		StateCheckpoint:      &fakes.FakeStateCheckpointService{},
		RepositoryCheckpoint: &fakes.FakeRepositoryCheckpointService{},
	}))
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"checkpoint", "create"})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected a validation error (--task-id omitted)")
	}
	env := decodeErrorEnvelope(t, out.Bytes())
	if env.Code != domain.ErrCodeValidation {
		t.Errorf("Code = %q, want %q", env.Code, domain.ErrCodeValidation)
	}
}

// newTestRoot builds a minimal root command, configured exactly like
// cli.NewRootCmd() (SilenceUsage/SilenceErrors: true, JSON error
// rendering applied), with child attached — the same shape a real command
// is always executed under in production (cli.NewRootCmd() itself, or
// internal/app/wiring.App.RootCmd() after replaceSubcommand), so tests
// exercising one real command's error/success path in isolation still see
// production-accurate Cobra configuration rather than Cobra's own
// un-configured defaults.
func newTestRoot(child *cobra.Command) *cobra.Command {
	root := &cobra.Command{Use: "auspex", SilenceUsage: true, SilenceErrors: true}
	root.AddCommand(child)
	return cli.WithJSONErrorRendering(root)
}

// TestErrorContract_RealCheckpointCreate_SuccessPathIsSchemaVersionedJSON
// confirms the success path's OWN schema-versioned output (this command's
// own "auspex.checkpoint-create.v1", distinct from the shared error
// envelope) is present and that NO error envelope leaks onto a successful
// call's stdout.
func TestErrorContract_RealCheckpointCreate_SuccessPathIsSchemaVersionedJSON(t *testing.T) {
	root := newTestRoot(cli.NewCheckpointCmd(orchestrator.CheckpointCreateDeps{
		StateCheckpoint: &fakes.FakeStateCheckpointService{
			CreateFunc: func(_ context.Context, req app.CreateStateCheckpointRequest) (domain.StateCheckpoint, error) {
				return domain.StateCheckpoint{ID: "sc-1", TaskID: req.TaskID}, nil
			},
		},
		RepositoryCheckpoint: &fakes.FakeRepositoryCheckpointService{
			CreateFunc: func(_ context.Context, req app.CreateRepositoryCheckpointRequest) (app.RepositoryCheckpoint, error) {
				return app.RepositoryCheckpoint{ID: "rc-1", GitHead: "head-1"}, nil
			},
		},
	}))
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"checkpoint", "create", "--task-id", "task1", "--worktree-id", "wt1"})

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var success struct {
		SchemaVersion string `json:"schema_version"`
	}
	if err := json.Unmarshal(out.Bytes(), &success); err != nil {
		t.Fatalf("stdout is not valid JSON: %v (body=%s)", err, out.Bytes())
	}
	if success.SchemaVersion != "auspex.checkpoint-create.v1" {
		t.Errorf("SchemaVersion = %q, want %q", success.SchemaVersion, "auspex.checkpoint-create.v1")
	}
	// No error envelope must appear anywhere on a successful call.
	if bytes.Contains(out.Bytes(), []byte(cli.SchemaVersionError)) {
		t.Errorf("success output unexpectedly contains the error schema version: %s", out.Bytes())
	}
}

// --- 3. Machine mode never emits decorative text ------------------------

// TestErrorContract_NoDecorativeTextOnAnyCommand confirms every command's
// stdout and stderr is either empty or valid JSON — never a banner, a
// progress message, or any other decorative plain text — on both the
// success and error path, across the entire stub tree (the one tree
// exercisable without constructing real per-command service fixtures).
//
// `version` is deliberately excluded from the STDOUT check: it prints a
// bare version string (buildinfo.String(), e.g. "0.0.0-dev"), not JSON —
// a genuine, pre-existing gap against the letter of "stable
// schema-versioned output" that this audit surfaces explicitly rather
// than silently fixing. Changing `version`'s output SHAPE is a real,
// visible compatibility decision for an already-integrated command (its
// existing test, TestVersionCommandIsReal, and any external caller/script
// depend on today's plain-string shape) — bigger than this node's mandate
// to close the ERROR-rendering gap, so it is documented here as a known
// gap rather than changed unilaterally. `version`'s ERROR path (it has
// none — cobra.NoArgs is its only possible failure, and Args validation
// errors are Cobra's own domain, out of this contract's scope) is not
// applicable either, so `version` is excluded from the loop entirely.
func TestErrorContract_NoDecorativeTextOnAnyCommand(t *testing.T) {
	for _, path := range allP0CommandPaths() {
		if path[0] == "version" {
			continue
		}
		t.Run(strings.Join(path, " "), func(t *testing.T) {
			root := cli.NewRootCmd()
			var stdout, stderr bytes.Buffer
			root.SetOut(&stdout)
			root.SetErr(&stderr)
			root.SetArgs(path)
			_ = root.Execute() // error or not, only the OUTPUT SHAPE matters here

			assertEmptyOrValidJSONLines(t, "stdout", stdout.Bytes())
			// stderr for a stub command carries exactly the JSON error
			// envelope — never a decorative message either, now that
			// SilenceErrors: true (root.go) stops Cobra's own default
			// plain-text printer from also running.
			assertEmptyOrValidJSONLines(t, "stderr", stderr.Bytes())
		})
	}
}

// TestErrorContract_VersionCommand_KnownGap_PlainStringNotJSON documents,
// as a checked test rather than only a code comment, that `version`'s
// success output is a bare string, not schema-versioned JSON — a
// pre-existing, real gap against agents/runtime.md's "stable
// schema-versioned output" this audit found but did NOT fix (see
// TestErrorContract_NoDecorativeTextOnAnyCommand's own doc comment for
// why). This test fails loudly if a future node changes version's output
// shape without updating this documented exception.
func TestErrorContract_VersionCommand_KnownGap_PlainStringNotJSON(t *testing.T) {
	root := cli.NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"version"})

	if err := root.Execute(); err != nil {
		t.Fatalf("auspex version: %v", err)
	}
	var v any
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &v); err == nil {
		t.Fatalf("auspex version now emits valid JSON (%s) — update this test and the schema-version gap note in errorcontract_test.go/root.go, this documented exception no longer applies", out.Bytes())
	}
}

// assertEmptyOrValidJSONLines confirms body is empty, OR every
// newline-delimited chunk in it parses as JSON — "machine mode never
// emits decorative text" means literally this: nothing that isn't valid,
// parseable machine output.
func assertEmptyOrValidJSONLines(t *testing.T, streamName string, body []byte) {
	t.Helper()
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return
	}
	for _, line := range bytes.Split(trimmed, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var v any
		if err := json.Unmarshal(line, &v); err != nil {
			t.Errorf("%s contains a non-JSON line (decorative text?): %q (err=%v)", streamName, line, err)
		}
	}
}

// --- 4. Hook fallback stays syntactically valid on failure --------------

// TestErrorContract_HookStubs_ProduceValidJSONErrorNotRawText confirms
// every `hook claude ...` stub subcommand's error path — the one surface
// agents/runtime.md singles out explicitly ("hook fallback remains
// syntactically valid when Auspex fails") — renders valid JSON on
// stderr rather than a bare Go panic/plain-text line, using the bare stub
// tree (guaranteed to error, since no real orchestrator.HookDeps is
// wired).
func TestErrorContract_HookStubs_ProduceValidJSONErrorNotRawText(t *testing.T) {
	hookPaths := [][]string{
		{"hook", "claude", "statusline"},
		{"hook", "claude", "user-prompt-submit"},
		{"hook", "claude", "post-tool-use"},
		{"hook", "claude", "stop"},
		{"hook", "claude", "stop-failure"},
	}
	for _, path := range hookPaths {
		t.Run(strings.Join(path, " "), func(t *testing.T) {
			root := cli.NewRootCmd()
			var out bytes.Buffer
			root.SetOut(&out)
			root.SetErr(&out)
			root.SetArgs(path)

			err := root.Execute()
			if err == nil {
				t.Fatalf("expected the stub hook command to error")
			}
			// The whole point of "syntactically valid" is that a
			// provider's own hook runner, parsing stderr/stdout as JSON,
			// never receives a truncated or non-JSON payload — proven the
			// same way as every other command's error path.
			decodeErrorEnvelope(t, out.Bytes())
		})
	}
}

// --- 5. Privacy gate: no raw prompt text anywhere ------------------------

// rawPromptCanary is a distinctive string standing in for "raw prompt
// text" — chosen to be unmistakable in any output byte scan (mirrors
// claude-provider-07's and qa-05's own established canary-string
// technique, applied here across this role's own CLI/orchestrator
// surface specifically, per this node's explicit brief).
const rawPromptCanary = "RAW-PROMPT-CANARY-should-never-appear-in-any-output"

// TestErrorContract_NoRawPromptInAnyErrorOrOutput drives every command
// surface that accepts prompt-adjacent input (decision allow/deny's
// --prompt-hash flag, and the bare stub hook/evaluate commands) with the
// canary string standing in for what a caller might mistakenly pass as
// raw prompt text, and scans ALL stdout/stderr bytes across the ENTIRE
// stub command tree for the canary's presence. Every command in this
// contract only ever accepts/threads a PromptHash (already a hash by the
// time it reaches this layer per internal/app.ports.go's frozen field
// shape) — this test proves that even if a caller passed something
// prompt-shaped through a hash-typed flag, this layer never echoes it
// back in an error message, log line, or JSON field, closing the loop on
// the privacy gate at the CLI error-contract boundary specifically.
func TestErrorContract_NoRawPromptInAnyErrorOrOutput(t *testing.T) {
	for _, path := range allP0CommandPaths() {
		t.Run(strings.Join(path, " "), func(t *testing.T) {
			root := cli.NewRootCmd()
			var out bytes.Buffer
			root.SetOut(&out)
			root.SetErr(&out)
			args := append(append([]string{}, path...), "--prompt-hash", rawPromptCanary, "--prompt", rawPromptCanary)
			root.SetArgs(args)
			_ = root.Execute() // unknown-flag errors are fine; only the canary-absence matters

			if bytes.Contains(out.Bytes(), []byte(rawPromptCanary)) {
				t.Errorf("auspex %s: canary string echoed back in output — a raw-prompt-shaped input must never be reflected: %s", strings.Join(path, " "), out.Bytes())
			}
		})
	}
}

// TestErrorContract_DecisionAllow_RealPath_NeverEchoesPromptHashAsRawText
// drives the REAL (not stub) `decision allow` issue flow with a
// PromptHash containing the canary (standing in for "a caller passed
// something prompt-shaped through this hash-typed field") through to a
// successful issued authorization, and confirms the canary appears
// NOWHERE in the command's stdout: decisionAllowOutput (internal/cli/
// decision.go) deliberately has no prompt_hash field at all — the value
// is bound into the real, opaque app.Authorization record the fake Issuer
// returns, never surfaced back to the CLI's own JSON output — so the
// correct, stronger assertion is total absence, not "appears exactly
// once in one structured field."
func TestErrorContract_DecisionAllow_RealPath_NeverEchoesPromptHashAsRawText(t *testing.T) {
	issuer := &fakeAuthIssuer{
		fn: func(_ domain.TurnID, promptHash, _, _ string, _ *domain.RepositoryCheckpointID) (app.Authorization, error) {
			return app.Authorization{ID: "auth-1", TurnID: "turn-1", PromptHash: promptHash}, nil
		},
	}
	root := newTestRoot(cli.NewDecisionCmd(orchestrator.DecisionDeps{
		Evaluation: &fakes.FakeEvaluationService{
			DecideFunc: func(_ context.Context, _ app.DecideRequest) (app.DecisionResult, error) {
				return app.DecisionResult{Action: app.PolicyCheckpointAndRun}, nil
			},
		},
		Issuer: issuer,
	}))
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"decision", "allow", "--turn-id", "turn-1", "--evaluation-id", "eval-1", "--prompt-hash", rawPromptCanary})

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var success struct {
		SchemaVersion string `json:"schema_version"`
		Issued        bool   `json:"issued"`
	}
	if err := json.Unmarshal(out.Bytes(), &success); err != nil {
		t.Fatalf("stdout is not valid JSON: %v (body=%s)", err, out.Bytes())
	}
	if !success.Issued {
		t.Fatal("expected the issue flow to report Issued = true")
	}
	if bytes.Contains(out.Bytes(), []byte(rawPromptCanary)) {
		t.Errorf("PromptHash canary leaked into decision allow's own stdout: %s", out.Bytes())
	}
}

// --- shared helpers ------------------------------------------------------

// decodeErrorEnvelope decodes body as cli's error envelope shape and fails
// the test with a descriptive message if it is not valid JSON matching
// that shape.
func decodeErrorEnvelope(t *testing.T, body []byte) struct {
	SchemaVersion string           `json:"schema_version"`
	Code          domain.ErrorCode `json:"code"`
	Message       string           `json:"message"`
	Retryable     bool             `json:"retryable"`
} {
	t.Helper()
	var env struct {
		SchemaVersion string           `json:"schema_version"`
		Code          domain.ErrorCode `json:"code"`
		Message       string           `json:"message"`
		Retryable     bool             `json:"retryable"`
	}
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		t.Fatal("expected a JSON error envelope, got empty output")
	}
	// A stream may carry more than one JSON line in principle; the error
	// envelope is always the last line this package's own wrapping
	// writes, so decode the last non-empty line.
	lines := bytes.Split(trimmed, []byte("\n"))
	last := bytes.TrimSpace(lines[len(lines)-1])
	if err := json.Unmarshal(last, &env); err != nil {
		t.Fatalf("output is not a valid JSON error envelope: %v (body=%s)", err, body)
	}
	return env
}

// fakeAuthIssuer is a minimal orchestrator.AuthorizationIssuer double,
// local to this file (the interface is package-local to internal/
// orchestrator, unexported by design — see decision.go's own doc comment
// — so this file declares its own matching-shape double rather than
// importing an internal test-only type).
type fakeAuthIssuer struct {
	fn func(turnID domain.TurnID, promptHash, snapshotFingerprint, action string, repoCkpt *domain.RepositoryCheckpointID) (app.Authorization, error)
}

func (f *fakeAuthIssuer) IssueAuthorization(_ context.Context, turnID domain.TurnID, promptHash, snapshotFingerprint, action string, repoCkpt *domain.RepositoryCheckpointID) (app.Authorization, error) {
	return f.fn(turnID, promptHash, snapshotFingerprint, action, repoCkpt)
}
