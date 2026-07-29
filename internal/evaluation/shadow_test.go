package evaluation

import (
	"testing"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/policy"
)

// TestApplyShadowEnforcement covers the unit contract shadow.go states:
// statistical enforcement-grade actions downgrade to WARN with the
// original action reported; the two fail-closed fact gates and every
// advisory action pass through untouched; disabled is a strict no-op.
func TestApplyShadowEnforcement(t *testing.T) {
	cases := []struct {
		name        string
		in          policy.Decision
		enabled     bool
		wantAction  app.PolicyAction
		wantWould   string // "" = expect nil
		wantReqConf bool
	}{
		{
			name:       "statistical block downgrades",
			in:         policy.Decision{Action: app.PolicyBlock, RequiresConfirmation: true, PolicyReasonCodes: []string{"some_statistical_gate"}},
			enabled:    true,
			wantAction: app.PolicyWarn,
			wantWould:  string(app.PolicyBlock),
		},
		{
			name:       "pause downgrades",
			in:         policy.Decision{Action: app.PolicyPause, PolicyReasonCodes: []string{"runway_emergency_threshold"}},
			enabled:    true,
			wantAction: app.PolicyWarn,
			wantWould:  string(app.PolicyPause),
		},
		{
			name:       "pause_and_auto_resume downgrades",
			in:         policy.Decision{Action: app.PolicyPauseAndAutoResume},
			enabled:    true,
			wantAction: app.PolicyWarn,
			wantWould:  string(app.PolicyPauseAndAutoResume),
		},
		{
			name:        "explicit deny is exempt",
			in:          policy.Decision{Action: app.PolicyBlock, RequiresConfirmation: true, PolicyReasonCodes: []string{"explicit_deny"}},
			enabled:     true,
			wantAction:  app.PolicyBlock,
			wantReqConf: true,
		},
		{
			name:        "integrity failure is exempt",
			in:          policy.Decision{Action: app.PolicyBlock, RequiresConfirmation: true, PolicyReasonCodes: []string{"integrity_failure"}},
			enabled:     true,
			wantAction:  app.PolicyBlock,
			wantReqConf: true,
		},
		{
			name:       "advisory action untouched",
			in:         policy.Decision{Action: app.PolicyCheckpointAndRun},
			enabled:    true,
			wantAction: app.PolicyCheckpointAndRun,
		},
		{
			name:        "disabled is a no-op",
			in:          policy.Decision{Action: app.PolicyPause, RequiresConfirmation: true},
			enabled:     false,
			wantAction:  app.PolicyPause,
			wantReqConf: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, would := applyShadowEnforcement(tc.in, tc.enabled)
			if out.Action != tc.wantAction {
				t.Errorf("Action = %q, want %q", out.Action, tc.wantAction)
			}
			if tc.wantWould == "" && would != nil {
				t.Errorf("wouldAction = %q, want nil", *would)
			}
			if tc.wantWould != "" && (would == nil || *would != tc.wantWould) {
				t.Errorf("wouldAction = %v, want %q", would, tc.wantWould)
			}
			if out.RequiresConfirmation != tc.wantReqConf {
				t.Errorf("RequiresConfirmation = %v, want %v", out.RequiresConfirmation, tc.wantReqConf)
			}
			// Shadow changes what Auspex DOES, never what it measured.
			if out.RiskScore != tc.in.RiskScore || out.Confidence != tc.in.Confidence || out.Severity != tc.in.Severity {
				t.Errorf("measurement fields changed: %+v vs input %+v", out, tc.in)
			}
		})
	}
}
