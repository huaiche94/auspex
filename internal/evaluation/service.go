package evaluation

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/policy"
	"github.com/huaiche94/auspex/internal/pricing"
	"github.com/huaiche94/auspex/internal/storage/sqlite"
)

// Service implements the frozen app.EvaluationService contract
// (internal/app/ports.go, ADD §9.9) by running the Predictor pipeline
// (Scope -> Token -> Quota -> Risk -> Policy, ADR-041) end-to-end for each
// EvaluateTurn call and persisting the result across this package's four
// migration-0040-0044 tables. See doc.go for the full pipeline-wiring,
// Decide-semantics, and ConsumeAuthorization-scope notes.
type Service struct {
	DB *sqlite.DB

	Source DataSource

	Scope   app.ScopeEstimator
	Tokens  app.TokenForecaster
	Quota   app.QuotaForecaster
	Risk    app.RiskCombiner
	Decider *policy.Decider

	Clock domain.Clock
	IDs   domain.IDGenerator

	// AuthorizationTTL is how long an issued Authorization remains valid
	// (Authorization.ExpiresAt = IssuedAt + AuthorizationTTL). Defaults to
	// DefaultAuthorizationTTL when zero.
	AuthorizationTTL time.Duration

	// Pricing is the ADR-043 cost-forecast price table ForecastCard uses
	// to turn persisted token quantiles into an estimated cost range.
	// Optional, unlike New's required dependencies: nil means
	// pricing.DefaultTable() (see forecastcard.go's pricingTable), so
	// every existing construction site keeps working unchanged and a
	// composition root that wants overridden prices sets this explicitly.
	Pricing *pricing.Table

	// Policy carries the policy.Decider's threshold configuration for
	// every Decide call this service makes (runway hit-probability gate,
	// and — ADR-043 increment 2 / DECISION_LOG.md D-08 — the
	// context-utilization thresholds, which ship ACTIVE by default).
	// Optional, following Pricing's exact convention: the zero value
	// normalizes to policy.DefaultConfig() inside Decide, so every
	// existing construction site keeps its behavior and D-08's factory
	// defaults apply out of the box. This field is the documented
	// programmatic override seam for adjusting or disabling the D-08
	// thresholds (e.g. Policy: policy.Config{
	// DisableContextUtilizationThresholds: true }). Wiring it from the
	// YAML config chain (internal/config) is a deliberate follow-up:
	// cmd/auspex/wire.go does not load internal/config at all today —
	// no production consumer of Config.Raw exists yet — and building
	// that plumbing for one threshold pair would be a config subsystem
	// grown ahead of need (Constitution §7 rule 10), exactly the same
	// judgment already recorded for Pricing's config override
	// (internal/pricing's package comment).
	Policy policy.Config
}

var _ app.EvaluationService = (*Service)(nil)

// DefaultAuthorizationTTL is the fallback one-time-authorization lifetime
// when Service.AuthorizationTTL is unset. No ADD section names an exact
// value for this vertical-slice wave; 5 minutes is a conservative, documented
// choice — long enough to cover the checkpoint-then-resume window a
// CHECKPOINT_AND_RUN/PAUSE_AND_AUTO_RESUME decision implies (ADD §17),
// short enough that a stale authorization cannot be replayed long after
// the state it was bound to has moved on.
const DefaultAuthorizationTTL = 5 * time.Minute

// New constructs a Service. db, source, scope, tokens, quota, risk,
// decider, clk, and ids must all be non-nil — New panics on a nil
// argument rather than deferring the failure to the first call, since a
// mis-wired Service is a construction-time bug, not a runtime degradation
// this package's fail-open discipline should paper over.
func New(db *sqlite.DB, source DataSource, scope app.ScopeEstimator, tokens app.TokenForecaster, quota app.QuotaForecaster, risk app.RiskCombiner, decider *policy.Decider, clk domain.Clock, ids domain.IDGenerator) *Service {
	if db == nil || source == nil || scope == nil || tokens == nil || quota == nil || risk == nil || decider == nil || clk == nil || ids == nil {
		panic("evaluation: New requires all dependencies to be non-nil")
	}
	return &Service{
		DB:               db,
		Source:           source,
		Scope:            scope,
		Tokens:           tokens,
		Quota:            quota,
		Risk:             risk,
		Decider:          decider,
		Clock:            clk,
		IDs:              ids,
		AuthorizationTTL: DefaultAuthorizationTTL,
	}
}

func (s *Service) authorizationTTL() time.Duration {
	if s.AuthorizationTTL <= 0 {
		return DefaultAuthorizationTTL
	}
	return s.AuthorizationTTL
}

// pipelineResult bundles one EvaluateTurn call's full pipeline output, so
// EvaluateTurn's persistence step can flatten it into feature_vectors/
// predictions/policy_decisions without recomputing anything.
type pipelineResult struct {
	scope    domain.ScopeEstimate
	tokens   domain.TokenForecast
	quotaFC  domain.QuotaForecast
	risk     app.CombineRiskResult
	decision policy.Decision
	features featuresSnapshot
	// modelID/effort are the session's observed identity (#20 Phase 0),
	// resolved once in runPipeline (it feeds the cost estimate below AND
	// the prediction-row stamp in EvaluateTurn — one query, two uses).
	modelID *string
	effort  *string
	// cost is the turn's estimated cost range under the observed model's
	// price family, nil when the token forecast carried no usable band —
	// the same estimate the forecast card renders, computed pre-decision
	// so the ADR-043 increment-3 budget rule can see it.
	cost *pricing.CostRange
}

// featuresSnapshot is the Go-level shape persisted verbatim (as JSON) into
// feature_vectors.features_json — every FeatureSource-derived input this
// evaluation's pipeline actually consulted, so a later re-derivation or
// debugging pass can see exactly what the prediction was based on without
// re-querying whatever storage DataSource itself wraps (which may have
// since changed).
type featuresSnapshot struct {
	RepositoryID domain.RepositoryID       `json:"repository_id"`
	TaskID       *domain.TaskID            `json:"task_id,omitempty"`
	TaskClass    string                    `json:"task_class"`
	Repository   *repositorySnapshot       `json:"repository,omitempty"`
	Session      *sessionSnapshotJSON      `json:"session,omitempty"`
	Progress     *progressSnapshotJSON     `json:"progress,omitempty"`
	Quota        []domain.QuotaObservation `json:"quota,omitempty"`
	Context      domain.ContextObservation `json:"context"`
}

type repositorySnapshot struct {
	TrackedFileCount int `json:"tracked_file_count"`
	DirtyFileCount   int `json:"dirty_file_count"`
}

type sessionSnapshotJSON struct {
	RetryRate *float64 `json:"retry_rate,omitempty"`
}

type progressSnapshotJSON struct {
	CriticalPathLength int `json:"critical_path_length"`
}

// EvaluateTurn implements app.EvaluationService. It runs the full
// Scope->Token->Quota->Risk->Policy pipeline for req and persists the
// result, returning the frozen app.Evaluation DTO.
func (s *Service) EvaluateTurn(ctx context.Context, req app.EvaluateTurnRequest) (app.Evaluation, error) {
	if err := validateEvaluateTurnRequest(req); err != nil {
		return app.Evaluation{}, err
	}

	result, err := s.runPipeline(ctx, req)
	if err != nil {
		return app.Evaluation{}, err
	}

	evaluationID := domain.EvaluationID(s.IDs.NewID())
	decisionID := domain.DecisionID(s.IDs.NewID())
	now := s.Clock.Now().UTC()
	nowStr := now.Format(time.RFC3339Nano)

	// #20 Phase 0: the turn's identity stamp (model/effort from the
	// session's latest observed identity — resolved once inside
	// runPipeline, which also priced the token forecast under it for the
	// ADR-043 increment-3 budget rule), family via the pricing table's
	// resolution, so the prediction row is labeled for calibration
	// stratification (#11) from day one. Fail-open — an unobserved
	// identity stamps NULLs, never blocks the evaluation.
	modelID, effort := result.modelID, result.effort
	var modelFamily *string
	if modelID != nil {
		if _, family := s.pricingTable().Price(*modelID); family != "" {
			modelFamily = &family
		}
	}
	provider := req.Provider

	featuresJSON, err := marshalFeatures(result.features)
	if err != nil {
		return app.Evaluation{}, err
	}
	predictionReasons, err := marshalReasonCodes(mergedPredictionReasonCodes(result))
	if err != nil {
		return app.Evaluation{}, err
	}
	decisionReasons, err := marshalReasonCodes(result.decision.ReasonCodes)
	if err != nil {
		return app.Evaluation{}, err
	}

	err = s.DB.WithTx(ctx, func(txCtx context.Context) error {
		if err := insertFeatureVector(txCtx, s.DB, featureVectorRow{
			TurnID:            req.TurnID,
			FeatureSetVersion: featureSetVersion,
			FeaturesJSON:      featuresJSON,
			CreatedAt:         nowStr,
		}); err != nil {
			return err
		}

		if err := insertPrediction(txCtx, s.DB, predictionRow{
			ID:                   evaluationID,
			TurnID:               req.TurnID,
			PredictorID:          predictorID,
			PredictorVersion:     predictorVersion,
			FeatureSetVersion:    featureSetVersion,
			TokenP50:             ptrInt64(result.tokens.TokensP50),
			TokenP80:             ptrInt64(result.tokens.TokensP80),
			TokenP90:             ptrInt64(result.tokens.TokensP90),
			FilesReadP50:         result.scope.FilesReadP50,
			FilesReadP90:         result.scope.FilesReadP90,
			FilesChangedP50:      result.scope.FilesChangedP50,
			FilesChangedP90:      result.scope.FilesChangedP90,
			LinesChangedP50:      result.scope.LinesChangedP50,
			LinesChangedP90:      result.scope.LinesChangedP90,
			QuotaRiskScore:       result.risk.QuotaRisk.Score,
			ContextRiskScore:     result.risk.ContextRisk.Score,
			CompletionRiskScore:  result.risk.CompletionRisk.Score,
			BlastRadiusRiskScore: result.risk.BlastRadiusRisk.Score,
			OverallRiskScore:     result.risk.OverallRisk.Score,
			// ADR-043 increment 2: the raw context projection is persisted
			// alongside its sigmoid risk expression (ContextRiskScore) so
			// the forecast card can render the actual projected
			// utilization percentage (migration 0045); nil stays NULL —
			// unknown is not zero.
			ProjectedContextUsedP90: result.quotaFC.ProjectedContextUsedP90,
			// #20 Phase 0 identity stamp (migration 0046).
			Provider:        &provider,
			ModelID:         modelID,
			ModelFamily:     modelFamily,
			Effort:          effort,
			Confidence:      result.risk.OverallRisk.Confidence,
			Calibrated:      result.risk.OverallRisk.Calibrated,
			ReasonCodesJSON: predictionReasons,
			CreatedAt:       nowStr,
		}); err != nil {
			return err
		}

		return insertPolicyDecision(txCtx, s.DB, policyDecisionRow{
			ID:                   decisionID,
			PredictionID:         evaluationID,
			RunwayForecastID:     nil, // this wave's DataSource surfaces domain.RunwayForecast directly, not a stored runway_forecasts row ID (predictor-06 owns that table)
			PolicyVersion:        policyVersion,
			Action:               string(result.decision.Action),
			Severity:             result.decision.Severity,
			RequiresConfirmation: result.decision.RequiresConfirmation,
			ReasonCodesJSON:      decisionReasons,
			DecidedAt:            nowStr,
		})
	})
	if err != nil {
		return app.Evaluation{}, fmt.Errorf("evaluation: persist EvaluateTurn result: %w", err)
	}

	return app.Evaluation{
		ID:          evaluationID,
		TurnID:      req.TurnID,
		CreatedAt:   now,
		Calibrated:  result.risk.OverallRisk.Calibrated,
		Confidence:  result.risk.OverallRisk.Confidence,
		ReasonCodes: mergedPredictionReasonCodes(result),
	}, nil
}

// mergedPredictionReasonCodes is the reason-code set persisted on the
// predictions row and returned on app.Evaluation — the risk pipeline's own
// OverallRisk.ReasonCodes, which internal/predictor/risk.RuleRiskCombiner
// already documents as the aggregate, most-informative reason-code set
// for a whole evaluation (risk/combiner.go: OverallRisk's Calibrated is
// the logical AND and Confidence the most conservative of the four
// components — its ReasonCodes mirrors that same "combine, don't just
// pick one" discipline for the same reason: overall.ReasonCodes already
// unions every component's own reason codes, by construction of
// RuleRiskCombiner).
func mergedPredictionReasonCodes(result pipelineResult) []domain.ReasonCode {
	return result.risk.OverallRisk.ReasonCodes
}

// GetEvaluation implements app.EvaluationService: looks up a previously
// persisted Evaluation by ID from the predictions table.
func (s *Service) GetEvaluation(ctx context.Context, id domain.EvaluationID) (app.Evaluation, error) {
	if id == "" {
		return app.Evaluation{}, &domain.Error{
			Code:      domain.ErrCodeValidation,
			Message:   "evaluation: GetEvaluation requires a non-empty EvaluationID",
			Retryable: false,
		}
	}

	row, err := getPrediction(ctx, s.DB, id)
	if err != nil {
		return app.Evaluation{}, err
	}

	reasons, err := unmarshalReasonCodes(row.ReasonCodesJSON)
	if err != nil {
		return app.Evaluation{}, err
	}

	createdAt, err := time.Parse(time.RFC3339Nano, row.CreatedAt)
	if err != nil {
		return app.Evaluation{}, &domain.Error{
			Code:      domain.ErrCodeIntegrity,
			Message:   "evaluation: predictions.created_at is not a valid timestamp",
			Retryable: false,
			Details:   map[string]string{"evaluation_id": string(id)},
		}
	}

	return app.Evaluation{
		ID:          row.ID,
		TurnID:      row.TurnID,
		CreatedAt:   createdAt,
		Calibrated:  row.Calibrated,
		Confidence:  row.Confidence,
		ReasonCodes: reasons,
	}, nil
}

// Decide implements app.EvaluationService. Per doc.go's "Decide: read-back,
// not recompute" section, Decide reads back the policy_decisions row
// EvaluateTurn already computed and stored for req.EvaluationID — it does
// not re-invoke internal/policy.Decider, since app.DecideRequest carries no
// risk/runway payload to recompute from.
func (s *Service) Decide(ctx context.Context, req app.DecideRequest) (app.DecisionResult, error) {
	if req.EvaluationID == "" {
		return app.DecisionResult{}, &domain.Error{
			Code:      domain.ErrCodeValidation,
			Message:   "evaluation: Decide requires a non-empty EvaluationID",
			Retryable: false,
		}
	}

	// Confirm the evaluation itself exists first, so a caller gets
	// ErrCodeNotFound against the evaluation it actually asked about
	// rather than a decision-table miss that reads as a different bug.
	if _, err := getPrediction(ctx, s.DB, req.EvaluationID); err != nil {
		return app.DecisionResult{}, err
	}

	row, err := getPolicyDecisionByPredictionID(ctx, s.DB, req.EvaluationID)
	if err != nil {
		return app.DecisionResult{}, err
	}

	return app.DecisionResult{
		ID:     row.ID,
		Action: app.PolicyAction(row.Action),
	}, nil
}

// ConsumeAuthorization implements app.EvaluationService. See doc.go's
// "ConsumeAuthorization scope note" for why this method is built in full
// under predictor-09 despite predictor-10 being the DAG's dedicated
// authorization-hardening node.
//
// Exactly-once consumption, expiry, and prompt/session binding are all
// checked inside one WithTx call (CONTRACT_FREEZE.md: "MUST be atomic with
// whatever action it authorizes"). The authoritative check for replay is
// markAuthorizationConsumed's conditional UPDATE ... WHERE consumed_at IS
// NULL (store.go) — a second concurrent or sequential consume attempt on
// the same ID affects zero rows and is rejected, never silently
// succeeding twice.
func (s *Service) ConsumeAuthorization(ctx context.Context, req app.ConsumeAuthorizationRequest) (app.Authorization, error) {
	if req.AuthorizationID == "" || req.TurnID == "" {
		return app.Authorization{}, &domain.Error{
			Code:      domain.ErrCodeValidation,
			Message:   "evaluation: ConsumeAuthorization requires a non-empty AuthorizationID and TurnID",
			Retryable: false,
		}
	}

	var result app.Authorization
	now := s.Clock.Now().UTC()

	err := s.DB.WithTx(ctx, func(txCtx context.Context) error {
		row, err := getAuthorizationForUpdate(txCtx, s.DB, req.AuthorizationID)
		if err != nil {
			return err
		}

		// Wrong-session/wrong-prompt binding (CONTRACT_FREEZE.md /
		// agents/predictor.md: "wrong prompt/wrong session authorization
		// rejected"). Checked before expiry/replay so a caller supplying
		// an ID that exists but belongs to a different turn/prompt gets
		// ErrCodeUnauthorized, not a confusing "expired"/"conflict" code.
		if row.TurnID != req.TurnID {
			return &domain.Error{
				Code:      domain.ErrCodeUnauthorized,
				Message:   "evaluation: authorization does not belong to the given turn",
				Retryable: false,
				Details:   map[string]string{"authorization_id": req.AuthorizationID},
			}
		}
		//
		// predictor-10 audit finding: this used to be
		// `req.PromptHash != "" && row.PromptHash != req.PromptHash`, i.e.
		// the check was skipped whenever the REQUEST omitted PromptHash,
		// regardless of what the authorization was actually bound to at
		// issuance. That let a caller who knows only AuthorizationID and
		// TurnID (e.g. leaked via logs, or reused across turns in the
		// same session) bypass prompt binding entirely by simply not
		// supplying PromptHash — defeating the point of binding an
		// authorization to a specific prompt. The binding must be
		// evaluated against what the authorization ROW was issued with,
		// not whether the caller chose to assert it: skip only when the
		// authorization itself carries no prompt hash (row.PromptHash ==
		// ""), which is the one legitimate "not applicable" case (an
		// authorization deliberately issued without prompt binding).
		if row.PromptHash != "" && row.PromptHash != req.PromptHash {
			return &domain.Error{
				Code:      domain.ErrCodeUnauthorized,
				Message:   "evaluation: authorization prompt hash does not match",
				Retryable: false,
				Details:   map[string]string{"authorization_id": req.AuthorizationID},
			}
		}

		// Replay: already consumed.
		if row.ConsumedAt != nil {
			return &domain.Error{
				Code:      domain.ErrCodeConflict,
				Message:   "evaluation: authorization has already been consumed",
				Retryable: false,
				Details:   map[string]string{"authorization_id": req.AuthorizationID},
			}
		}

		// Expiry (clock-bound, via s.Clock — never time.Now() directly, so
		// this is deterministically testable with a fake clock).
		expiresAt, err := time.Parse(time.RFC3339Nano, row.ExpiresAt)
		if err != nil {
			return &domain.Error{
				Code:      domain.ErrCodeIntegrity,
				Message:   "evaluation: authorizations.expires_at is not a valid timestamp",
				Retryable: false,
				Details:   map[string]string{"authorization_id": req.AuthorizationID},
			}
		}
		if !now.Before(expiresAt) {
			return &domain.Error{
				Code:      domain.ErrCodeUnauthorized,
				Message:   "evaluation: authorization has expired",
				Retryable: false,
				Details:   map[string]string{"authorization_id": req.AuthorizationID},
			}
		}

		consumedAtStr := now.Format(time.RFC3339Nano)
		ok, err := markAuthorizationConsumed(txCtx, s.DB, req.AuthorizationID, consumedAtStr)
		if err != nil {
			return err
		}
		if !ok {
			// Lost a race with a concurrent consumer between the read
			// above and this UPDATE: same outcome as "already consumed".
			return &domain.Error{
				Code:      domain.ErrCodeConflict,
				Message:   "evaluation: authorization has already been consumed",
				Retryable: false,
				Details:   map[string]string{"authorization_id": req.AuthorizationID},
			}
		}

		issuedAt, err := time.Parse(time.RFC3339Nano, row.IssuedAt)
		if err != nil {
			return &domain.Error{
				Code:      domain.ErrCodeIntegrity,
				Message:   "evaluation: authorizations.issued_at is not a valid timestamp",
				Retryable: false,
				Details:   map[string]string{"authorization_id": req.AuthorizationID},
			}
		}

		result = app.Authorization{
			ID:                     row.ID,
			TurnID:                 row.TurnID,
			PromptHash:             row.PromptHash,
			SnapshotFingerprint:    row.SnapshotFingerprint,
			Decision:               row.Decision,
			RepositoryCheckpointID: row.RepositoryCheckpointID,
			IssuedAt:               issuedAt,
			ExpiresAt:              expiresAt,
			ConsumedAt:             &now,
		}
		return nil
	})
	if err != nil {
		return app.Authorization{}, err
	}
	return result, nil
}

// IssueAuthorization creates a new one-time Authorization bound to
// turnID/promptHash/snapshotFingerprint/decision, persisted to the
// authorizations table (migration 0044). Not part of the frozen
// app.EvaluationService interface (which has no "issue" method — only
// ConsumeAuthorization) but required for ConsumeAuthorization to ever have
// something real to consume; agents/predictor.md deliverable #12 names
// "one-time authorization issuance/consumption" as one deliverable, and
// CONTRACT_FREEZE.md's migration 0044 comment describes issuance as this
// role's storage-layer responsibility. A future EvaluateTurn/Decide caller
// (e.g. internal/orchestrator, once it wires the real Service) is expected
// to call this after a PolicyAction that requires one (CHECKPOINT_AND_RUN,
// PAUSE_AND_AUTO_RESUME, etc.) — EvaluateTurn itself does not call this
// automatically this wave, since which actions require an authorization
// and what SnapshotFingerprint/RepositoryCheckpointID to bind are
// orchestration-layer decisions outside this package's Boundary (no
// checkpoint creation, no Git commands).
func (s *Service) IssueAuthorization(ctx context.Context, turnID domain.TurnID, promptHash, snapshotFingerprint, decision string, repoCheckpointID *domain.RepositoryCheckpointID) (app.Authorization, error) {
	if turnID == "" {
		return app.Authorization{}, &domain.Error{
			Code:      domain.ErrCodeValidation,
			Message:   "evaluation: IssueAuthorization requires a non-empty TurnID",
			Retryable: false,
		}
	}

	id := s.IDs.NewID()
	now := s.Clock.Now().UTC()
	expiresAt := now.Add(s.authorizationTTL())

	err := insertAuthorization(ctx, s.DB, authorizationRow{
		ID:                     id,
		TurnID:                 turnID,
		PromptHash:             promptHash,
		SnapshotFingerprint:    snapshotFingerprint,
		Decision:               decision,
		RepositoryCheckpointID: repoCheckpointID,
		IssuedAt:               now.Format(time.RFC3339Nano),
		ExpiresAt:              expiresAt.Format(time.RFC3339Nano),
		ConsumedAt:             nil,
	})
	if err != nil {
		return app.Authorization{}, err
	}

	return app.Authorization{
		ID:                     id,
		TurnID:                 turnID,
		PromptHash:             promptHash,
		SnapshotFingerprint:    snapshotFingerprint,
		Decision:               decision,
		RepositoryCheckpointID: repoCheckpointID,
		IssuedAt:               now,
		ExpiresAt:              expiresAt,
		ConsumedAt:             nil,
	}, nil
}

// sessionIdentity resolves the session's latest observed identity
// (provider_sessions.model/effort, kept fresh by statusline ingest via
// SessionBootstrapper's COALESCE upsert — #20 Phase 0) for stamping onto
// the prediction row. Fail-open by design: a session never observed (or
// any query error) resolves to nils — unknown is not zero, and identity
// resolution must never fail an evaluation. Turn-level accuracy note: a
// /model or /fast switch is observed at the next statusline refresh, so
// the stamp can lag one refresh behind an intra-turn switch; the
// turn-end truth for effort additionally lands on the turn.completed
// event (NormalizeStop) for calibration cross-checks.
func (s *Service) sessionIdentity(ctx context.Context, sessionID domain.SessionID) (modelID, effort *string) {
	q := sqlite.QuerierFromContext(ctx, s.DB)
	var m, e sql.NullString
	if err := q.QueryRowContext(ctx,
		`SELECT model, effort FROM provider_sessions WHERE id = ?`,
		string(sessionID),
	).Scan(&m, &e); err != nil {
		return nil, nil
	}
	return nullStringPtr(m), nullStringPtr(e)
}

func validateEvaluateTurnRequest(req app.EvaluateTurnRequest) error {
	var missing []string
	if req.SessionID == "" {
		missing = append(missing, "SessionID")
	}
	if req.TurnID == "" {
		missing = append(missing, "TurnID")
	}
	if req.Provider == "" {
		missing = append(missing, "Provider")
	}
	if len(missing) == 0 {
		return nil
	}
	details := ""
	for i, m := range missing {
		if i > 0 {
			details += ","
		}
		details += m
	}
	return &domain.Error{
		Code:      domain.ErrCodeValidation,
		Message:   "evaluation: EvaluateTurn request is missing required fields",
		Retryable: false,
		Details:   map[string]string{"missing_fields": details},
	}
}

func ptrInt64(v int64) *int64 {
	return &v
}
