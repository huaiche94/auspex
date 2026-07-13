package idgen_test

import (
	"regexp"
	"testing"

	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/idgen"
)

// uuidPattern matches the canonical 8-4-4-4-12 hex UUID string shape.
var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func TestNewReturnsDomainIDGenerator(t *testing.T) {
	// The explicit domain.IDGenerator type is the point of this test (it
	// documents/asserts New() satisfies the domain contract); keep it
	// even though staticcheck can infer the type from New()'s signature.
	var _ domain.IDGenerator = idgen.New() //nolint:staticcheck // explicit interface assertion is intentional
}

func TestNewIDIsNonEmptyAndUUIDShaped(t *testing.T) {
	g := idgen.New()

	id := g.NewID()
	if id == "" {
		t.Fatal("NewID() returned an empty string")
	}
	if !uuidPattern.MatchString(id) {
		t.Fatalf("NewID() = %q, does not look like a UUID", id)
	}
}

func TestNewIDIsVersion7(t *testing.T) {
	g := idgen.New()

	id := g.NewID()
	// The version nibble is the first character of the third group.
	if len(id) < 15 || id[14] != '7' {
		t.Fatalf("NewID() = %q, expected UUID version 7 (14th char '7')", id)
	}
}

func TestNewIDIsUnique(t *testing.T) {
	g := idgen.New()

	seen := make(map[string]bool)
	const n = 1000
	for i := 0; i < n; i++ {
		id := g.NewID()
		if seen[id] {
			t.Fatalf("NewID() produced a duplicate: %q", id)
		}
		seen[id] = true
	}
}

func TestNewIDIsRoughlyTimeOrdered(t *testing.T) {
	g := idgen.New()

	first := g.NewID()
	second := g.NewID()

	// UUIDv7 embeds a millisecond timestamp in its leading bits, so
	// lexicographic string comparison is monotonic non-decreasing for IDs
	// generated in sequence (barring the same millisecond, where ordering
	// still holds due to monotonic random bits in google/uuid's NewV7).
	if second < first {
		t.Fatalf("expected UUIDv7 IDs to be non-decreasing: first=%q second=%q", first, second)
	}
}
