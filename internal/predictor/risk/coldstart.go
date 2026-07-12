package risk

// Coefficients for ADD §16.2's "Initial explainable formula". Named exactly
// after the formula's own variable names (not renamed/abbreviated) so this
// file can be checked against the ADD text line by line.
const (
	// quota_risk / context_risk: sigmoid((projected_p90 - sigmoidMidpoint) / sigmoidScale).
	sigmoidMidpoint = 85.0
	sigmoidScale    = 7.0

	// completion_risk = clamp(completionBase + sum(coefficient * term), 0, 1).
	completionBase                        = 0.10
	completionFilesChangedP90Coefficient  = 0.04
	completionLinesChangedP90Coefficient  = 0.0004
	completionIntegrationTestsCoefficient = 0.12
	completionMigrationCoefficient        = 0.15
	completionCrossLayerCoefficient       = 0.10
	completionOpenEndedScopeCoefficient   = 0.15
	completionRecentRetryRateCoefficient  = 0.20
	completionTestFailureRateCoefficient  = 0.10
	completionProgressBlockersCoefficient = 0.10

	// blast_radius_risk = clamp(blastRadiusBase + sum(coefficient * term), 0, 1).
	blastRadiusBase                    = 0.05
	blastRadiusFilesChangedP90Coeff    = 0.03
	blastRadiusCrossProjectCoefficient = 0.15
	blastRadiusMigrationCoefficient    = 0.20
	blastRadiusSecuritySensitiveCoeff  = 0.15
	blastRadiusPublicAPIChangeCoeff    = 0.10
)
