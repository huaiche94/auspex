package clock_test

import (
	"testing"
	"time"

	"github.com/huaiche94/preflight/internal/clock"
	"github.com/huaiche94/preflight/internal/domain"
)

func TestNewReturnsDomainClock(t *testing.T) {
	var _ domain.Clock = clock.New()
}

func TestNowReturnsSaneRecentTime(t *testing.T) {
	c := clock.New()

	before := time.Now()
	got := c.Now()
	after := time.Now()

	if got.Before(before) || got.After(after) {
		t.Fatalf("Now() = %v, want a time between %v and %v", got, before, after)
	}

	// Sanity bound: the clock must not be wildly off (e.g. epoch zero or a
	// stale/fake value). Anything within a year of "now" is acceptable —
	// this is a smoke test, not an NTP check.
	if got.Year() < time.Now().Year()-1 {
		t.Fatalf("Now() = %v, looks implausible for the current date", got)
	}
}

func TestNowAdvances(t *testing.T) {
	c := clock.New()

	t1 := c.Now()
	time.Sleep(1 * time.Millisecond)
	t2 := c.Now()

	if !t2.After(t1) {
		t.Fatalf("expected Now() to advance: t1=%v t2=%v", t1, t2)
	}
}
