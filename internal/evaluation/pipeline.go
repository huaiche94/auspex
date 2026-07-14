package evaluation

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/policy"
	"github.com/huaiche94/auspex/internal/pricing"
)

// runPipeline runs the full Scope -> Token -> Quota -> Risk -> Policy
// chain (ADR-041) for req, resolving every non-frozen-DTO input through
// s.Source. It returns an error only for a genuine failure of one of the
// pipeline stages (ScopeEstimator/TokenForecaster/QuotaForecaster/
// RiskCombiner) or s.Source itself — every stage's own missing-input
// degradation (cold-start, low confidence, etc.) is handled internally by
// that stage per its own documented contract, not surfaced as an error
// here.
func (s *Service) runPipeline(ctx context.Context, req app.EvaluateTurnRequest) (pipelineResult, error) {
	resolved, err := s.Source.Resolve(ctx, req.SessionID)
	if err != nil {
		return pipelineResult{}, fmt.Errorf("evaluation: resolve session %s: %w", req.SessionID, err)
	}

	scopeEstimate, err := s.Scope.EstimateScope(ctx, app.EstimateScopeRequest{
		SessionID:    req.SessionID,
		TaskID:       resolved.TaskID,
		RepositoryID: resolved.RepositoryID,
	})
	if err != nil {
		return pipelineResult{}, fmt.Errorf("evaluation: EstimateScope: %w", err)
	}

	tokenForecast, err := s.Tokens.ForecastTokens(ctx, app.ForecastTokensRequest{
		SessionID: req.SessionID,
		Scope:     scopeEstimate,
	})
	if err != nil {
		return pipelineResult{}, fmt.Errorf("evaluation: ForecastTokens: %w", err)
	}

	quotaObs, err := s.Source.Quota(ctx, req.SessionID)
	if err != nil {
		return pipelineResult{}, fmt.Errorf("evaluation: load quota observations: %w", err)
	}
	contextObs, err := s.Source.Context(ctx, req.SessionID)
	if err != nil {
		return pipelineResult{}, fmt.Errorf("evaluation: load context observation: %w", err)
	}

	quotaForecast, err := s.Quota.ForecastQuota(ctx, app.ForecastQuotaRequest{
		SessionID:     req.SessionID,
		TokenForecast: tokenForecast,
		Quota:         quotaObs,
		Context:       contextObs,
	})
	if err != nil {
		return pipelineResult{}, fmt.Errorf("evaluation: ForecastQuota: %w", err)
	}

	riskResult, err := s.Risk.Combine(ctx, app.CombineRiskRequest{
		Scope:         scopeEstimate,
		TokenForecast: tokenForecast,
		QuotaForecast: quotaForecast,
	})
	if err != nil {
		return pipelineResult{}, fmt.Errorf("evaluation: Combine risk: %w", err)
	}

	runwayForecast, hasRunway, err := s.Source.RunwayForecast(ctx, req.SessionID)
	if err != nil {
		return pipelineResult{}, fmt.Errorf("evaluation: load runway forecast: %w", err)
	}
	if !hasRunway {
		// No runway forecast yet (brand-new session, or
		// GracefulPauseService.Observe has not run) is a cold-start gap,
		// not an error — the zero domain.RunwayForecast has
		// Calibrated==false, which policy.Decider's runway gate already
		// treats as "not pause-worthy from this signal," per policy's own
		// documented fail-open discipline.
		runwayForecast = domain.RunwayForecast{}
	}

	priorConfirmed, err := s.Source.PriorRunwayHitConfirmed(ctx, req.SessionID)
	if err != nil {
		return pipelineResult{}, fmt.Errorf("evaluation: load prior runway hit confirmation: %w", err)
	}

	// #20 Phase 0 identity + ADR-043 increment-3 cost estimate: resolve
	// the session's observed model once (fail-open — nils when never
	// observed), price the token forecast under it, and let the policy
	// stage compare that range against any user-declared budget. This is
	// the same estimate the forecast card renders read-back; computing it
	// here too keeps the budget decision and the displayed number from
	// ever disagreeing (same table, same model, same quantiles).
	modelID, effort := s.sessionIdentity(ctx, req.SessionID)
	var costEstimate *pricing.CostRange
	if tokenForecast.TokensP50 > 0 || tokenForecast.TokensP90 > 0 {
		model := ""
		if modelID != nil {
			model = *modelID
		}
		if cr, ok := s.pricingTable().EstimateTurnCost(model, tokenForecast.TokensP50, tokenForecast.TokensP90); ok {
			costEstimate = &cr
		}
	}

	decision := s.Decider.Decide(policy.DecideRequest{
		Risk:                    riskResult,
		Runway:                  runwayForecast,
		PriorRunwayHitConfirmed: priorConfirmed,
		// Cost feeds the ADR-043 increment-3 cost-budget rule
		// (policy/costbudget.go); nil (no estimate) keeps the rule
		// silent, and an unset budget keeps it inactive regardless.
		Cost: costEstimate,
		// Quota feeds the ADR-043 increment-2 / D-08 context-utilization
		// threshold rule (policy/context.go): policy sees the raw
		// projected context P90 directly, because D-08's thresholds are
		// defined on the utilization percentage itself, not on the
		// sigmoid risk term RiskCombiner derives from the same forecast.
		Quota: quotaForecast,
		// Config is Service.Policy — the programmatic override seam for
		// D-08's adjustable/disable-able thresholds; the zero value
		// normalizes to policy.DefaultConfig() (thresholds active, per
		// the owner-approved factory posture).
		Config: s.Policy,
	})

	classification, promptFeatures, err := s.Source.Classification(ctx, req.SessionID, resolved.TaskID)
	if err != nil {
		return pipelineResult{}, fmt.Errorf("evaluation: load classification: %w", err)
	}
	_ = promptFeatures // consulted by scope/token stages via DataSource already; retained here only for the features snapshot below if ever extended

	snapshot := featuresSnapshot{
		RepositoryID: resolved.RepositoryID,
		TaskID:       resolved.TaskID,
		TaskClass:    string(classification.Class),
		Quota:        quotaObs,
		Context:      contextObs,
	}
	if repoFeat, ok, err := s.Source.Repository(ctx, resolved.RepositoryID); err != nil {
		return pipelineResult{}, fmt.Errorf("evaluation: load repository features: %w", err)
	} else if ok {
		snapshot.Repository = &repositorySnapshot{
			TrackedFileCount: repoFeat.TrackedFileCount,
			DirtyFileCount:   repoFeat.DirtyFileCount,
		}
	}
	if sessFeat, ok, err := s.Source.Session(ctx, req.SessionID); err != nil {
		return pipelineResult{}, fmt.Errorf("evaluation: load session features: %w", err)
	} else if ok {
		snapshot.Session = &sessionSnapshotJSON{RetryRate: sessFeat.RetryRate}
	}
	if progFeat, ok, err := s.Source.Progress(ctx, resolved.TaskID); err != nil {
		return pipelineResult{}, fmt.Errorf("evaluation: load progress features: %w", err)
	} else if ok {
		snapshot.Progress = &progressSnapshotJSON{CriticalPathLength: progFeat.CriticalPathLength}
	}

	return pipelineResult{
		scope:    scopeEstimate,
		tokens:   tokenForecast,
		quotaFC:  quotaForecast,
		risk:     riskResult,
		decision: decision,
		features: snapshot,
		modelID:  modelID,
		effort:   effort,
		cost:     costEstimate,
	}, nil
}

func marshalFeatures(snap featuresSnapshot) (string, error) {
	b, err := json.Marshal(snap)
	if err != nil {
		return "", fmt.Errorf("evaluation: marshal features snapshot: %w", err)
	}
	return string(b), nil
}
