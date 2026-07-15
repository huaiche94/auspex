package features

import "github.com/huaiche94/auspex/internal/domain"

// This file is the SINGLE source of truth for the provider.turn.started
// prompt-feature payload schema (issue #50 item 1). Before it existed, the
// ~20 snake_case payload keys lived as raw string literals in TWO packages
// that had to stay in lockstep by hand: the WRITER
// (internal/telemetry/claude/normalizer.go, NormalizeUserPromptSubmit) and
// the READER (internal/evaluation/datasource_sql.go, latestPromptFeatures).
// A typo or a dropped key on either side compiled clean and decoded to
// false/0 — indistinguishable from an HONEST absence of signal — silently
// collapsing classification back toward TaskClassUnknown (the exact bug #42
// fixed and #50 warns will regress). Routing both sides through
// EncodePromptFeatures/DecodePromptFeatures means every key string exists in
// exactly one place below: a dropped or mistyped key is now a compile-time
// break or a round-trip-test failure, never a silent decode-to-false
// (see prompt_codec_test.go's round-trip and writer/reader-agreement tests).
//
// Privacy (Constitution §7 rule 2): the codec encodes ONLY the already-
// derived fields of PromptFeatures — booleans, counts, and the fixed-
// alphabet SHA-256 hex digest. No raw prompt text, substring, or n-gram
// crosses this boundary; PromptFeatures' own type contract already forbids
// them (its only string fields are the digest and the Confidence enum).
//
// Layout is FLAT (keys at payload top level), deliberately: pre-#50 events
// persisted these 20 keys flat, and this codec MUST keep decoding those old
// events correctly rather than regressing them to all-false /
// TaskClassUnknown (BINDING constraint, #50). #51's optional "nest the
// features under one key so bulk readers skip them" micro-optimization is
// NOT pursued here — it would require Decode to fall back to flat keys for
// old events, and preserving historical decodability is not worth that risk
// for a read that already only touches the latest row. Deferred as a
// possible follow-up.

// PromptFeatureVersion marks the feature-EXTRACTION era of a persisted
// prompt-feature snapshot (issue #50 items 2 & 3). It is a payload-only
// label, distinct from evaluation/store.go's featureSetVersion (which
// versions the feature_vectors/predictions feature SET, a different table
// and a different concept). A single version covers the whole extraction
// era because #47's vocabulary widening and the ADD §14.7 approx-token
// estimator swap shipped as one logical generation of ExtractPromptFeatures:
//   - events CARRYING this key were extracted by the current logic (the
//     #47-widened / #49-tuned vocabulary and the §14.7 estimator);
//   - events LACKING this key predate #50's stamping and mix extraction
//     eras (pre-#42 size-only, #42's initial vocabulary + bytes/4 estimator,
//     #47's widened vocabulary) with no way to tell which — so calibration
//     (#11 / #20 Phase 2) MUST NOT blend measured features or approx-token
//     scales across that boundary silently. Absent is honestly unknown-era,
//     never assumed to be the current era.
//
// The value is a fixed compile-time constant (fixed alphabet, provably
// independent of any prompt's content), which is why the privacy pin tests
// may allow it as a payload string exactly as they allow prompt_sha256.
const PromptFeatureVersion = "prompt-features.v1"

// PromptFeatureVersionKey is the payload key under which PromptFeatureVersion
// is stamped. Exported so downstream consumers (retention/research
// calibration) can detect the extraction-era boundary by the sanctioned
// name instead of a hand-typed literal.
const PromptFeatureVersionKey = "prompt_feature_version"

// Payload key constants — the one and only place the wire schema is spelled
// out. Writer and reader reference these through Encode/Decode; neither ever
// hand-types a key again.
const (
	keyPromptSHA256          = "prompt_sha256"
	keyPromptByteLength      = "prompt_byte_length"
	keyPromptApproxTokens    = "prompt_approx_tokens"
	keyPromptRuneCount       = "prompt_rune_count"
	keyPromptLineCount       = "prompt_line_count"
	keyExplicitPathCount     = "explicit_path_count"
	keyListItemCount         = "list_item_count"
	keyAcceptanceCriteria    = "acceptance_criteria_count"
	keyHasFixVerb            = "has_fix_verb"
	keyHasImplementVerb      = "has_implement_verb"
	keyHasRefactorVerb       = "has_refactor_verb"
	keyHasInvestigateVerb    = "has_investigate_verb"
	keyHasMigrateVerb        = "has_migrate_verb"
	keyMentionsTests         = "mentions_tests"
	keyMentionsSchemaOrAPI   = "mentions_schema_or_api"
	keyMentionsSecurity      = "mentions_security"
	keyMentionsPerformance   = "mentions_performance"
	keyMentionsDocumentation = "mentions_documentation"
	keyLongDocumentInd       = "long_document_indicator"
	keyQuestionInd           = "question_indicator"
	keyOpenEndedInd          = "open_ended_indicator"
	keyCrossLayerInd         = "cross_layer_indicator"
	keyRepositoryWideInd     = "repository_wide_indicator"
)

// EncodePromptFeatures projects a PromptFeatures into the flat payload map
// the writer persists on provider.turn.started (issue #50 item 1). It is the
// counterpart of DecodePromptFeatures — the two are proven inverse by the
// round-trip test. Every value is a bool, an int, or the fixed-alphabet hash
// string; nothing here can carry raw prompt text (Constitution §7 rule 2).
// TokenConfidence is intentionally NOT encoded: it is an invariant
// (ApproxTokens is always the §14.7 estimate, so confidence is always
// ConfidenceLow), which Decode restores rather than reads back.
//
// The extraction-version tag is stamped here so it rides with the derived
// features and shares their persistence gate (the normalizer only encodes
// when features were genuinely extracted); an event without the tag is
// therefore exactly an event whose features were never extracted OR one
// persisted before #50 — both correctly read as "unknown era".
func EncodePromptFeatures(pf PromptFeatures) map[string]any {
	return map[string]any{
		PromptFeatureVersionKey: PromptFeatureVersion,

		keyPromptSHA256:       pf.SHA256Hex,
		keyPromptByteLength:   pf.ByteLength,
		keyPromptApproxTokens: pf.ApproxTokens,
		keyPromptRuneCount:    pf.RuneCount,
		keyPromptLineCount:    pf.LineCount,

		keyExplicitPathCount:  pf.ExplicitPathCount,
		keyListItemCount:      pf.ListItemCount,
		keyAcceptanceCriteria: pf.AcceptanceCriteriaCount,

		keyHasFixVerb:         pf.HasFixVerb,
		keyHasImplementVerb:   pf.HasImplementVerb,
		keyHasRefactorVerb:    pf.HasRefactorVerb,
		keyHasInvestigateVerb: pf.HasInvestigateVerb,
		keyHasMigrateVerb:     pf.HasMigrateVerb,

		keyMentionsTests:         pf.MentionsTests,
		keyMentionsSchemaOrAPI:   pf.MentionsSchemaOrAPI,
		keyMentionsSecurity:      pf.MentionsSecurity,
		keyMentionsPerformance:   pf.MentionsPerformance,
		keyMentionsDocumentation: pf.MentionsDocumentation,
		keyLongDocumentInd:       pf.LongDocumentIndicator,
		keyQuestionInd:           pf.QuestionIndicator,
		keyOpenEndedInd:          pf.OpenEndedIndicator,
		keyCrossLayerInd:         pf.CrossLayerIndicator,
		keyRepositoryWideInd:     pf.RepositoryWideIndicator,
	}
}

// DecodePromptFeatures reconstructs a PromptFeatures from a persisted
// provider.turn.started payload map (issue #50 item 1). It is tolerant by
// design: a key ABSENT from the payload decodes to its zero value — the
// honest state for a signal that was never captured (an event persisted
// before that field existed), never a fabricated default (unknown is not
// zero). This is what keeps pre-#50 flat-key events decoding correctly
// instead of collapsing to all-false.
//
// Numeric coercion accepts both int (an in-process map straight from Encode,
// as the round-trip test exercises) and float64 (a map produced by
// json.Unmarshal, as the real reader receives), so the codec is symmetric
// whether or not the payload made a JSON round trip.
//
// TokenConfidence is always set to domain.ConfidenceLow, mirroring
// ExtractPromptFeatures: ApproxTokens is a §14.7 estimate, never an exact
// count, so its confidence is a fixed invariant, not a persisted value.
func DecodePromptFeatures(payload map[string]any) PromptFeatures {
	return PromptFeatures{
		SHA256Hex:       decodeString(payload, keyPromptSHA256),
		ByteLength:      decodeInt(payload, keyPromptByteLength),
		ApproxTokens:    decodeInt(payload, keyPromptApproxTokens),
		RuneCount:       decodeInt(payload, keyPromptRuneCount),
		LineCount:       decodeInt(payload, keyPromptLineCount),
		TokenConfidence: domain.ConfidenceLow,

		ExplicitPathCount:       decodeInt(payload, keyExplicitPathCount),
		ListItemCount:           decodeInt(payload, keyListItemCount),
		AcceptanceCriteriaCount: decodeInt(payload, keyAcceptanceCriteria),

		HasFixVerb:         decodeBool(payload, keyHasFixVerb),
		HasImplementVerb:   decodeBool(payload, keyHasImplementVerb),
		HasRefactorVerb:    decodeBool(payload, keyHasRefactorVerb),
		HasInvestigateVerb: decodeBool(payload, keyHasInvestigateVerb),
		HasMigrateVerb:     decodeBool(payload, keyHasMigrateVerb),

		MentionsTests:           decodeBool(payload, keyMentionsTests),
		MentionsSchemaOrAPI:     decodeBool(payload, keyMentionsSchemaOrAPI),
		MentionsSecurity:        decodeBool(payload, keyMentionsSecurity),
		MentionsPerformance:     decodeBool(payload, keyMentionsPerformance),
		MentionsDocumentation:   decodeBool(payload, keyMentionsDocumentation),
		LongDocumentIndicator:   decodeBool(payload, keyLongDocumentInd),
		QuestionIndicator:       decodeBool(payload, keyQuestionInd),
		OpenEndedIndicator:      decodeBool(payload, keyOpenEndedInd),
		CrossLayerIndicator:     decodeBool(payload, keyCrossLayerInd),
		RepositoryWideIndicator: decodeBool(payload, keyRepositoryWideInd),
	}
}

// PromptFeatureVersionFromPayload reports the extraction-era tag a payload
// carries, if any. ok=false means the key is absent — the event predates
// #50's stamping (see PromptFeatureVersion's doc): honestly unknown-era,
// which calibration must not silently treat as the current era.
func PromptFeatureVersionFromPayload(payload map[string]any) (string, bool) {
	v, ok := payload[PromptFeatureVersionKey].(string)
	return v, ok
}

// decodeInt reads an integer-valued payload key, tolerating both the int a
// direct Encode map holds and the float64 json.Unmarshal produces. A missing
// or non-numeric key yields 0 (unknown is not zero: an event that never
// carried this count).
func decodeInt(payload map[string]any, key string) int {
	switch v := payload[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	default:
		return 0
	}
}

// decodeBool reads a boolean payload key. A missing or non-boolean key
// yields false — the honest zero value for a signal an old event never
// captured (matches the pre-#42 read-back behavior exactly).
func decodeBool(payload map[string]any, key string) bool {
	b, _ := payload[key].(bool)
	return b
}

// decodeString reads a string payload key (only the SHA-256 digest here). A
// missing or non-string key yields "".
func decodeString(payload map[string]any, key string) string {
	s, _ := payload[key].(string)
	return s
}
