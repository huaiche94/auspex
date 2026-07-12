package domain

import "time"

type MeasurementSource string

const (
	SourceProviderEvent MeasurementSource = "provider_event"
	SourceProviderAPI   MeasurementSource = "provider_api"
	SourceStatusLine    MeasurementSource = "status_line"
	SourceHook          MeasurementSource = "hook"
	SourceGit           MeasurementSource = "git"
	SourceProcess       MeasurementSource = "process"
	SourceDerived       MeasurementSource = "derived"
	SourceEstimated     MeasurementSource = "estimated"
	SourceImported      MeasurementSource = "imported"
)

type Confidence string

const (
	ConfidenceExact       Confidence = "exact"
	ConfidenceHigh        Confidence = "high"
	ConfidenceMedium      Confidence = "medium"
	ConfidenceLow         Confidence = "low"
	ConfidenceUnavailable Confidence = "unavailable"
)

type Measurement[T any] struct {
	Value      T
	Source     MeasurementSource
	Confidence Confidence
	ObservedAt time.Time
	StaleAfter time.Time
}
