package v1

import "testing"

// Schema-version strings are a public compatibility commitment (ADD §12.1,
// CONTRACT_FREEZE.md). A change here requires an ADR (Constitution §3).
func TestSchemaVersionStrings(t *testing.T) {
	cases := map[string]string{
		SchemaVersionEvent:                "auspex.event.v1",
		SchemaVersionProgressTree:         "auspex.progress-tree.v1",
		SchemaVersionStateCheckpoint:      "auspex.state-checkpoint.v1",
		SchemaVersionRepositoryCheckpoint: "auspex.repository-checkpoint.v1",
		SchemaVersionPause:                "auspex.pause.v1",
		SchemaVersionAPI:                  "auspex.api.v1",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("schema version mismatch: got %q, want %q", got, want)
		}
	}
}

func TestEventTypeTaxonomySample(t *testing.T) {
	cases := map[EventType]string{
		EventProgressNodeCompleted:  "progress.node.completed",
		EventStateCheckpointCreated: "state.checkpoint.created",
		EventPauseWakeTriggered:     "pause.wake.triggered",
		EventProviderRateLimitHit:   "provider.rate_limit.hit",
	}
	for got, want := range cases {
		if string(got) != want {
			t.Errorf("event type mismatch: got %q, want %q", got, want)
		}
	}
}
