package domain

import "time"

type UsageObservation struct {
	InputTokens              *int64
	CachedInputTokens        *int64
	CacheCreationInputTokens *int64
	CacheReadInputTokens     *int64
	OutputTokens             *int64
	ReasoningTokens          *int64
	TotalObservedTokens      *int64
	BillableTokenEquivalent  *float64
	Source                   MeasurementSource
	Confidence               Confidence
	ObservedAt               time.Time
}

type QuotaObservation struct {
	ID            string
	SessionID     SessionID
	Provider      string
	LimitID       string
	LimitName     string
	UsedPercent   *float64
	WindowSeconds *int64
	ResetsAt      *time.Time
	Reached       bool
	Source        MeasurementSource
	Confidence    Confidence
	ObservedAt    time.Time
}

type ContextObservation struct {
	ID           string
	SessionID    SessionID
	TurnID       *TurnID
	UsedTokens   *int64
	WindowTokens *int64
	UsedPercent  *float64
	Source       MeasurementSource
	Confidence   Confidence
	ObservedAt   time.Time
}

type RunwayForecast struct {
	LimitID                        string
	HorizonSeconds                 int64
	CurrentUsedPercent             *float64
	HitProbability                 *float64
	RiskScore                      float64
	Calibrated                     bool
	Confidence                     Confidence
	SampleCount                    int64
	BurnRateP50                    *float64
	BurnRateP90                    *float64
	EstimatedTimeToLimitP50Seconds *int64
	EstimatedTimeToLimitP90Seconds *int64
	QuotaObservedAt                *time.Time
	ReasonCodes                    []string
}
