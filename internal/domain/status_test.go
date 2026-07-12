package domain

import "testing"

// Wire-string values are a compatibility commitment (ADD §9.3-9.4, §9.7-9.8).
// A typo here silently breaks every consumer that pattern-matches on the string.
func TestStatusWireStrings(t *testing.T) {
	cases := []struct{ got, want string }{
		{string(TurnPending), "pending"},
		{string(TurnCompleted), "completed"},
		{string(NodeInProgress), "in_progress"},
		{string(NodeCompleted), "completed"},
		{string(NodeBlocked), "blocked"},
		{string(PauseSleeping), "sleeping"},
		{string(PauseResumed), "resumed"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("wire string mismatch: got %q, want %q", c.got, c.want)
		}
	}
}

func TestFailureClassWireStrings(t *testing.T) {
	if string(FailureProviderRateLimit) != "provider_rate_limit" {
		t.Errorf("got %q, want provider_rate_limit", FailureProviderRateLimit)
	}
	if string(FailureStateCheckpoint) != "state_checkpoint" {
		t.Errorf("got %q, want state_checkpoint", FailureStateCheckpoint)
	}
}
