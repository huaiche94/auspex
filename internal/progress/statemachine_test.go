package progress_test

import (
	"errors"
	"testing"

	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/progress"
)

// TestValidTransitions_Allowed exercises every edge in the frozen adjacency
// list documented in statemachine.go, so the table itself is regression
// tested edge-by-edge rather than only smoke-tested.
func TestValidTransitions_Allowed(t *testing.T) {
	valid := []struct {
		from domain.ProgressNodeStatus
		to   domain.ProgressNodeStatus
	}{
		{domain.NodePending, domain.NodeReady},
		{domain.NodePending, domain.NodeBlocked},
		{domain.NodePending, domain.NodeSkipped},
		{domain.NodeReady, domain.NodeInProgress},
		{domain.NodeReady, domain.NodeBlocked},
		{domain.NodeReady, domain.NodeSkipped},
		{domain.NodeInProgress, domain.NodeCheckpointing},
		{domain.NodeInProgress, domain.NodePaused},
		{domain.NodeInProgress, domain.NodeFailed},
		{domain.NodeInProgress, domain.NodeBlocked},
		{domain.NodeCheckpointing, domain.NodeCompleted},
		{domain.NodeCheckpointing, domain.NodeFailed},
		{domain.NodeCheckpointing, domain.NodeInProgress},
		{domain.NodePaused, domain.NodeInProgress},
		{domain.NodePaused, domain.NodeBlocked},
		{domain.NodePaused, domain.NodeSkipped},
		{domain.NodeBlocked, domain.NodeReady},
		{domain.NodeBlocked, domain.NodeInProgress},
		{domain.NodeBlocked, domain.NodeSkipped},
		{domain.NodeFailed, domain.NodeInProgress},
	}
	for _, tc := range valid {
		t.Run(string(tc.from)+"->"+string(tc.to), func(t *testing.T) {
			if err := progress.ValidateTransition(tc.from, tc.to); err != nil {
				t.Fatalf("expected %s -> %s to be valid, got error: %v", tc.from, tc.to, err)
			}
		})
	}
}

// TestInvalidTransitions_Rejected is the DAG's explicit "invalid state
// transition rejected" required test. It covers: skipping stages entirely,
// moving backward past the frozen backbone, transitioning out of a
// terminal state, and an identity (self) transition.
func TestInvalidTransitions_Rejected(t *testing.T) {
	invalid := []struct {
		name string
		from domain.ProgressNodeStatus
		to   domain.ProgressNodeStatus
	}{
		{"pending_directly_to_completed", domain.NodePending, domain.NodeCompleted},
		{"pending_directly_to_in_progress", domain.NodePending, domain.NodeInProgress},
		{"ready_directly_to_completed", domain.NodeReady, domain.NodeCompleted},
		{"ready_directly_to_checkpointing", domain.NodeReady, domain.NodeCheckpointing},
		{"in_progress_directly_to_completed", domain.NodeInProgress, domain.NodeCompleted},
		{"completed_to_anything", domain.NodeCompleted, domain.NodeInProgress},
		{"completed_to_failed", domain.NodeCompleted, domain.NodeFailed},
		{"skipped_to_anything", domain.NodeSkipped, domain.NodeReady},
		{"failed_to_completed_directly", domain.NodeFailed, domain.NodeCompleted},
		{"failed_to_checkpointing_directly", domain.NodeFailed, domain.NodeCheckpointing},
		{"checkpointing_to_ready_backward", domain.NodeCheckpointing, domain.NodeReady},
		{"blocked_to_completed_directly", domain.NodeBlocked, domain.NodeCompleted},
		{"self_transition_pending", domain.NodePending, domain.NodePending},
		{"self_transition_in_progress", domain.NodeInProgress, domain.NodeInProgress},
		{"unknown_source_status", domain.ProgressNodeStatus("bogus"), domain.NodeReady},
		{"unknown_target_status", domain.NodePending, domain.ProgressNodeStatus("bogus")},
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			err := progress.ValidateTransition(tc.from, tc.to)
			if err == nil {
				t.Fatalf("expected %s -> %s to be rejected, got nil error", tc.from, tc.to)
			}
			var domErr *domain.Error
			if !errors.As(err, &domErr) {
				t.Fatalf("expected error to unwrap to *domain.Error, got %T", err)
			}
			if domErr.Code != domain.ErrCodeValidation {
				t.Fatalf("expected ErrCodeValidation, got %s", domErr.Code)
			}
			if domErr.Retryable {
				t.Fatalf("expected invalid transition to be non-retryable")
			}
		})
	}
}

func TestIsTerminal(t *testing.T) {
	terminal := []domain.ProgressNodeStatus{domain.NodeCompleted, domain.NodeSkipped}
	for _, s := range terminal {
		if !progress.IsTerminal(s) {
			t.Errorf("expected %s to be terminal", s)
		}
	}

	nonTerminal := []domain.ProgressNodeStatus{
		domain.NodePending, domain.NodeReady, domain.NodeInProgress,
		domain.NodeCheckpointing, domain.NodePaused, domain.NodeFailed, domain.NodeBlocked,
	}
	for _, s := range nonTerminal {
		if progress.IsTerminal(s) {
			t.Errorf("expected %s to NOT be terminal", s)
		}
	}
}

func TestAllowedTransitions_MatchesValidateTransition(t *testing.T) {
	all := []domain.ProgressNodeStatus{
		domain.NodePending, domain.NodeReady, domain.NodeInProgress,
		domain.NodeCheckpointing, domain.NodePaused, domain.NodeCompleted,
		domain.NodeFailed, domain.NodeSkipped, domain.NodeBlocked,
	}
	for _, from := range all {
		allowed := progress.AllowedTransitions(from)
		allowedSet := map[domain.ProgressNodeStatus]bool{}
		for _, to := range allowed {
			allowedSet[to] = true
			if err := progress.ValidateTransition(from, to); err != nil {
				t.Errorf("AllowedTransitions(%s) includes %s but ValidateTransition rejects it: %v", from, to, err)
			}
		}
		for _, to := range all {
			if allowedSet[to] {
				continue
			}
			if err := progress.ValidateTransition(from, to); err == nil {
				t.Errorf("ValidateTransition(%s, %s) accepted but AllowedTransitions(%s) does not list it", from, to, from)
			}
		}
	}
}
