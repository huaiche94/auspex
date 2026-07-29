package policy

import (
	"testing"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/pricing"
)

func fp(v float64) *float64 { return &v }

// TestApplySessionBudget covers sessionbudget.go's tier and overlay
// contract, mirroring the costbudget test discipline.
func TestApplySessionBudget(t *testing.T) {
	band := &pricing.CostRange{LowUSD: 1, HighUSD: 5}
	cfg := Config{SessionBudgetUSD: 10}

	cases := []struct {
		name       string
		base       Decision
		req        DecideRequest
		cfg        Config
		wantAction app.PolicyAction
		wantCode   domain.ReasonCode // "" = no envelope code expected
	}{
		{
			name:       "no budget declared -> untouched",
			base:       Decision{Action: app.PolicyRun},
			req:        DecideRequest{SessionSpentUSD: fp(100), Cost: band},
			cfg:        Config{},
			wantAction: app.PolicyRun,
		},
		{
			name:       "spend unknown -> untouched (unknown is not zero)",
			base:       Decision{Action: app.PolicyRun},
			req:        DecideRequest{SessionSpentUSD: nil, Cost: band},
			cfg:        cfg,
			wantAction: app.PolicyRun,
		},
		{
			name:       "fits -> untouched",
			base:       Decision{Action: app.PolicyRun},
			req:        DecideRequest{SessionSpentUSD: fp(3), Cost: band},
			cfg:        cfg,
			wantAction: app.PolicyRun,
		},
		{
			name:       "reservation breach -> WARN",
			base:       Decision{Action: app.PolicyRun},
			req:        DecideRequest{SessionSpentUSD: fp(6), Cost: band}, // 6+5 > 10
			cfg:        cfg,
			wantAction: app.PolicyWarn,
			wantCode:   domain.ReasonSessionBudgetReservationExceeded,
		},
		{
			name:       "exhausted -> PAUSE",
			base:       Decision{Action: app.PolicyRun},
			req:        DecideRequest{SessionSpentUSD: fp(10), Cost: band},
			cfg:        cfg,
			wantAction: app.PolicyPause,
			wantCode:   domain.ReasonSessionBudgetExhausted,
		},
		{
			name:       "exhausted without a band still fires",
			base:       Decision{Action: app.PolicyRun},
			req:        DecideRequest{SessionSpentUSD: fp(12), Cost: nil},
			cfg:        cfg,
			wantAction: app.PolicyPause,
			wantCode:   domain.ReasonSessionBudgetExhausted,
		},
		{
			name:       "stronger base -> annotation only",
			base:       Decision{Action: app.PolicyBlock, PolicyReasonCodes: []string{"explicit_deny"}},
			req:        DecideRequest{SessionSpentUSD: fp(12), Cost: band},
			cfg:        cfg,
			wantAction: app.PolicyBlock,
			wantCode:   domain.ReasonSessionBudgetExhausted,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := applySessionBudget(tc.base, tc.req, tc.cfg)
			if out.Action != tc.wantAction {
				t.Errorf("Action = %q, want %q", out.Action, tc.wantAction)
			}
			has := false
			for _, c := range out.ReasonCodes {
				if c == tc.wantCode {
					has = true
				}
			}
			if tc.wantCode != "" && !has {
				t.Errorf("ReasonCodes = %v, want %q present", out.ReasonCodes, tc.wantCode)
			}
			if tc.wantCode == "" && len(out.ReasonCodes) != len(tc.base.ReasonCodes) {
				t.Errorf("ReasonCodes changed on a no-tier case: %v", out.ReasonCodes)
			}
			if out.Probability != nil {
				t.Error("Probability non-nil — a budget comparison is never a probability")
			}
		})
	}
}
