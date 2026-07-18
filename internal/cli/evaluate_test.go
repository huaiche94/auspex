// evaluate_test.go: issue #14's CLI-surface tests — `auspex evaluate`
// happy path (human + schema-versioned JSON), validation errors through
// the typed error contract, the privacy gate at the CLI boundary (raw
// prompt text in via --prompt-file/stdin, never out), and the statusline
// --emit-line flag (one compact line with the flag, byte-identical
// ingest-only behavior without it — issue #12 friction #2's fix). The
// deeper privacy proof against real on-disk DB bytes lives in
// internal/integrationtest (leakage-scanner style); these tests cover the
// command surface with fakes, like every other real command's tests here.
package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/cli"
	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/evaluation"
	"github.com/huaiche94/auspex/internal/orchestrator"
	"github.com/huaiche94/auspex/internal/pricing"
	"github.com/huaiche94/auspex/internal/testutil/fakes"
)

// --- local doubles -------------------------------------------------------

// fixedTestClock/seqTestIDs are the package-local deterministic
// Clock/IDGenerator pattern every test suite in this repo builds for
// itself (internal/orchestrator/hooks_test.go's fixedClock/
// sequentialHookIDs are unexported to that package's test binary).
type fixedTestClock struct{}

func (fixedTestClock) Now() time.Time {
	return time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC)
}

type seqTestIDs struct{ n int }

func (g *seqTestIDs) NewID() string {
	g.n++
	return fmt.Sprintf("cli-id-%d", g.n)
}

type fakeCardSource struct {
	card evaluation.ForecastCard
	err  error
}

func (f *fakeCardSource) ForecastCard(_ context.Context, _ domain.EvaluationID) (evaluation.ForecastCard, error) {
	if f.err != nil {
		return evaluation.ForecastCard{}, f.err
	}
	return f.card, nil
}

func (f *fakeCardSource) LatestForecastCard(_ context.Context, _ domain.SessionID) (evaluation.ForecastCard, bool, error) {
	if f.err != nil {
		return evaluation.ForecastCard{}, false, f.err
	}
	return f.card, true, nil
}

func testCLIForecastCard() evaluation.ForecastCard {
	p50, p80, p90 := int64(8000), int64(20000), int64(45000)
	return evaluation.ForecastCard{
		EvaluationID: "eval-1",
		TurnID:       "turn-1",
		TokensP50:    &p50, TokensP80: &p80, TokensP90: &p90,
		Cost:             &pricing.CostRange{LowUSD: 0.02, HighUSD: 0.68, ModelFamily: pricing.DefaultFamily, Source: pricing.SourceDefaultTable},
		OverallRiskScore: 0.42,
		Confidence:       domain.ConfidenceLow,
		PolicyAction:     app.PolicyWarn,
	}
}

func evaluateTestDeps(capturedHash *string) orchestrator.HookDeps {
	return orchestrator.HookDeps{
		Clock: fixedTestClock{},
		IDs:   &seqTestIDs{},
		Evaluation: &fakes.FakeEvaluationService{
			EvaluateTurnFunc: func(_ context.Context, req app.EvaluateTurnRequest) (app.Evaluation, error) {
				if capturedHash != nil {
					*capturedHash = req.PromptHash
				}
				return app.Evaluation{ID: "eval-1", TurnID: req.TurnID, Confidence: domain.ConfidenceLow}, nil
			},
			DecideFunc: func(_ context.Context, _ app.DecideRequest) (app.DecisionResult, error) {
				return app.DecisionResult{Action: app.PolicyWarn}, nil
			},
		},
		Forecast: &fakeCardSource{card: testCLIForecastCard()},
	}
}

// --- evaluate: happy paths ----------------------------------------------

func TestEvaluate_JSONOutput_SchemaVersionedWithNullProbability(t *testing.T) {
	root := newTestRoot(cli.NewEvaluateCmd(evaluateTestDeps(nil)))
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetIn(strings.NewReader("add a retry loop"))
	root.SetArgs([]string{"evaluate", "--session-id", "sess-1", "--prompt-file", "-", "--json"})

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &decoded); err != nil {
		t.Fatalf("stdout is not valid JSON: %v (body=%s)", err, out.Bytes())
	}
	if decoded["schema_version"] != "auspex.evaluate.v1" {
		t.Errorf("schema_version = %v, want auspex.evaluate.v1", decoded["schema_version"])
	}
	if decoded["policy_action"] != "WARN" {
		t.Errorf("policy_action = %v, want WARN", decoded["policy_action"])
	}
	if decoded["label"] != "uncalibrated estimate" {
		t.Errorf("label = %v, want \"uncalibrated estimate\" (Constitution principle #2)", decoded["label"])
	}
	// Constitution principle #2's load-bearing assertion: the probability
	// KEY must be present AND null — not omitted, not a number.
	prob, present := decoded["probability"]
	if !present {
		t.Fatal("probability key absent — cold-start output must emit probability: null explicitly")
	}
	if prob != nil {
		t.Fatalf("probability = %v, want null (nothing is calibrated this phase)", prob)
	}
	if decoded["calibrated"] != false {
		t.Errorf("calibrated = %v, want false", decoded["calibrated"])
	}
	tokens, ok := decoded["tokens"].(map[string]any)
	if !ok {
		t.Fatalf("tokens = %v, want an object", decoded["tokens"])
	}
	if tokens["p50"] != float64(8000) {
		t.Errorf("tokens.p50 = %v, want 8000", tokens["p50"])
	}
	cost, ok := decoded["cost"].(map[string]any)
	if !ok {
		t.Fatalf("cost = %v, want an object", decoded["cost"])
	}
	if cost["estimate"] != true {
		t.Errorf("cost.estimate = %v, want true — cost is always labeled an estimate (ADR-043)", cost["estimate"])
	}
	// ADR-043 increment 2: the context block is always present alongside
	// the card, and an unknown projection is an explicit null — never 0,
	// never an absent key (ADD principle 1).
	ctxBlock, ok := decoded["context"].(map[string]any)
	if !ok {
		t.Fatalf("context = %v, want an object", decoded["context"])
	}
	proj, present := ctxBlock["projected_p90_used_percent"]
	if !present {
		t.Fatal("context.projected_p90_used_percent key absent — an unknown projection must serialize as an explicit null")
	}
	if proj != nil {
		t.Errorf("context.projected_p90_used_percent = %v, want null for a card without a projection", proj)
	}
	if ctxBlock["warn_threshold_exceeded"] != false || ctxBlock["checkpoint_threshold_exceeded"] != false {
		t.Errorf("threshold flags = %v/%v, want false/false without a recorded threshold decision",
			ctxBlock["warn_threshold_exceeded"], ctxBlock["checkpoint_threshold_exceeded"])
	}
}

// TestEvaluate_JSONOutput_ContextThresholdState (ADR-043 increment 2,
// D-08): a card carrying a persisted context projection with a recorded
// checkpoint-threshold state serializes both onto the evaluate JSON
// surface.
func TestEvaluate_JSONOutput_ContextThresholdState(t *testing.T) {
	deps := evaluateTestDeps(nil)
	card := testCLIForecastCard()
	proj := 97.0
	card.ContextProjectedP90 = &proj
	card.ContextCheckpointThresholdExceeded = true
	deps.Forecast = &fakeCardSource{card: card}

	root := newTestRoot(cli.NewEvaluateCmd(deps))
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"evaluate", "--session-id", "sess-1", "--json"})

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &decoded); err != nil {
		t.Fatalf("stdout is not valid JSON: %v (body=%s)", err, out.Bytes())
	}
	ctxBlock, ok := decoded["context"].(map[string]any)
	if !ok {
		t.Fatalf("context = %v, want an object", decoded["context"])
	}
	if ctxBlock["projected_p90_used_percent"] != float64(97) {
		t.Errorf("context.projected_p90_used_percent = %v, want 97", ctxBlock["projected_p90_used_percent"])
	}
	if ctxBlock["checkpoint_threshold_exceeded"] != true {
		t.Errorf("context.checkpoint_threshold_exceeded = %v, want true", ctxBlock["checkpoint_threshold_exceeded"])
	}
	if ctxBlock["warn_threshold_exceeded"] != false {
		t.Errorf("context.warn_threshold_exceeded = %v, want false", ctxBlock["warn_threshold_exceeded"])
	}
}

func TestEvaluate_HumanOutput_RendersCard(t *testing.T) {
	root := newTestRoot(cli.NewEvaluateCmd(evaluateTestDeps(nil)))
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"evaluate", "--session-id", "sess-1"})

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	got := out.String()
	for _, want := range []string{"uncalibrated estimate", "policy: WARN", "evaluation eval-1"} {
		if !strings.Contains(got, want) {
			t.Errorf("human output missing %q:\n%s", want, got)
		}
	}
}

func TestEvaluate_CardUnavailable_StillReportsDecision(t *testing.T) {
	deps := evaluateTestDeps(nil)
	deps.Forecast = &fakeCardSource{err: &domain.Error{Code: domain.ErrCodeUnavailable, Message: "down"}}
	root := newTestRoot(cli.NewEvaluateCmd(deps))
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"evaluate", "--session-id", "sess-1", "--json"})

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &decoded); err != nil {
		t.Fatalf("stdout is not valid JSON: %v", err)
	}
	if decoded["card_available"] != false {
		t.Errorf("card_available = %v, want false", decoded["card_available"])
	}
	if decoded["policy_action"] != "WARN" {
		t.Errorf("policy_action = %v, want WARN (the evaluation itself succeeded)", decoded["policy_action"])
	}
	if _, present := decoded["cost"]; present {
		t.Error("cost present without a card — degraded output must not fabricate numbers")
	}
}

// --- evaluate: validation -------------------------------------------------

func TestEvaluate_Validation(t *testing.T) {
	t.Run("missing session id", func(t *testing.T) {
		root := newTestRoot(cli.NewEvaluateCmd(evaluateTestDeps(nil)))
		var out bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&out)
		root.SetArgs([]string{"evaluate"})

		err := root.Execute()
		var derr *domain.Error
		if !errors.As(err, &derr) || derr.Code != domain.ErrCodeValidation {
			t.Fatalf("err = %v, want ErrCodeValidation", err)
		}
		env := decodeErrorEnvelope(t, out.Bytes())
		if env.Code != domain.ErrCodeValidation {
			t.Errorf("envelope Code = %q, want validation", env.Code)
		}
	})
	t.Run("unreadable prompt file", func(t *testing.T) {
		root := newTestRoot(cli.NewEvaluateCmd(evaluateTestDeps(nil)))
		var out bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&out)
		root.SetArgs([]string{"evaluate", "--session-id", "sess-1", "--prompt-file", "/no/such/file"})

		err := root.Execute()
		var derr *domain.Error
		if !errors.As(err, &derr) || derr.Code != domain.ErrCodeValidation {
			t.Fatalf("err = %v, want ErrCodeValidation", err)
		}
		if derr.Details["path"] != "/no/such/file" {
			t.Errorf("Details[path] = %q, want the path (and only the path)", derr.Details["path"])
		}
	})
}

// --- evaluate: privacy at the CLI boundary --------------------------------

// TestEvaluate_PrivacyGate_RawPromptNeverInOutput feeds a canary prompt
// through --prompt-file - (stdin) and asserts (1) the evaluation saw only
// a 64-hex-char hash, and (2) the canary appears NOWHERE in stdout/stderr
// of either output mode — the same canary technique as
// errorcontract_test.go's TestErrorContract_NoRawPromptInAnyErrorOrOutput,
// applied to the one command that genuinely ingests raw prompt text.
func TestEvaluate_PrivacyGate_RawPromptNeverInOutput(t *testing.T) {
	const canary = "RAW-PROMPT-CANARY-evaluate-should-never-appear-in-any-output"
	for _, mode := range [][]string{
		{"evaluate", "--session-id", "sess-1", "--prompt-file", "-"},
		{"evaluate", "--session-id", "sess-1", "--prompt-file", "-", "--json"},
	} {
		var capturedHash string
		root := newTestRoot(cli.NewEvaluateCmd(evaluateTestDeps(&capturedHash)))
		var out bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&out)
		root.SetIn(strings.NewReader(canary))
		root.SetArgs(mode)

		if err := root.Execute(); err != nil {
			t.Fatalf("%v: Execute: %v", mode, err)
		}
		if bytes.Contains(out.Bytes(), []byte(canary)) {
			t.Errorf("%v: canary prompt text leaked into command output:\n%s", mode, out.Bytes())
		}
		if len(capturedHash) != 64 {
			t.Errorf("%v: PromptHash = %q (len %d), want a 64-char hex digest", mode, capturedHash, len(capturedHash))
		}
		if strings.Contains(capturedHash, "CANARY") {
			t.Errorf("%v: raw text reached the evaluation request", mode)
		}
	}
}

// --- statusline --emit-line -----------------------------------------------

// statuslinePayload is a minimal, valid status-line snapshot (the
// fixture-file corpus lives under testdata/provider-events and is read by
// orchestrator's own tests; the CLI layer only needs a representative
// payload for flag-behavior coverage).
const statuslinePayload = `{"session_id":"sess-1","model":{"id":"claude-opus-4-1","display_name":"Opus 4.1"}}`

func statuslineTestDeps() orchestrator.HookDeps {
	return orchestrator.HookDeps{
		Clock:    fixedTestClock{},
		IDs:      &seqTestIDs{},
		Forecast: &fakeCardSource{card: testCLIForecastCard()},
	}
}

// TestStatusLine_DefaultRemainsByteIdenticalIngestOnly is the issue-#14
// compatibility gate: WITHOUT --emit-line the command's stdout/stderr
// must be byte-identical to today's ingest-only behavior — exactly zero
// bytes — even with a forecast source fully wired.
func TestStatusLine_DefaultRemainsByteIdenticalIngestOnly(t *testing.T) {
	root := newTestRoot(cli.NewHookClaudeCmd(statuslineTestDeps()))
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetIn(strings.NewReader(statuslinePayload))
	root.SetArgs([]string{"claude", "statusline"})

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if stdout.Len() != 0 {
		t.Errorf("default statusline stdout = %q, want empty (byte-identical to pre-issue-#14 ingest-only behavior)", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("default statusline stderr = %q, want empty", stderr.String())
	}
}

func TestStatusLine_EmitLinePrintsOneCompactLine(t *testing.T) {
	root := newTestRoot(cli.NewHookClaudeCmd(statuslineTestDeps()))
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetIn(strings.NewReader(statuslinePayload))
	root.SetArgs([]string{"claude", "statusline", "--emit-line"})

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// Byte-exact ANSI pins (issue #29), written out rather than imported
	// from the renderer so a regression cannot rewrite its expectations.
	const (
		reset = "\x1b[0m"
		brand = "\x1b[36max»" + reset
		sep   = "\x1b[2m │ " + reset
	)
	// D-15 (#41): no token segment, and the policy segment is just the
	// active action; the test card carries no context projection and the
	// payload no measurement, so the line is model + badge only.
	badgeWarn := "\x1b[33m⚠ WARN" + reset
	if want := brand + " Opus 4.1" + sep + badgeWarn + "\n"; out.String() != want {
		t.Errorf("emit-line output = %q, want %q", out.String(), want)
	}
}

func TestStatusLine_EmitLine_MalformedInputStillPrintsFallback(t *testing.T) {
	root := newTestRoot(cli.NewHookClaudeCmd(statuslineTestDeps()))
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetIn(strings.NewReader("{not json"))
	root.SetArgs([]string{"claude", "statusline", "--emit-line"})

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute should fail open, got: %v", err)
	}
	if out.String() != "\x1b[36max»\x1b[0m\n" {
		t.Errorf("output = %q, want the bare fallback line — the status bar must never go blank", out.String())
	}
}
