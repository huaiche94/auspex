package v1

import "testing"

// Schema-version strings are a public compatibility commitment (ADD §12.1,
// CONTRACT_FREEZE.md). A change here requires an ADR (Constitution §3).
func TestSchemaVersionStrings(t *testing.T) {
	cases := map[string]string{
		SchemaVersionEvent:                "preflight.event.v1",
		SchemaVersionProgressTree:         "preflight.progress-tree.v1",
		SchemaVersionStateCheckpoint:      "preflight.state-checkpoint.v1",
		SchemaVersionRepositoryCheckpoint: "preflight.repository-checkpoint.v1",
		SchemaVersionPause:                "preflight.pause.v1",
		SchemaVersionAPI:                  "preflight.api.v1",
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
