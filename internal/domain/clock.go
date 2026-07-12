package domain

import (
	"context"
	"time"
)

type Clock interface {
	Now() time.Time
}

type IDGenerator interface {
	NewID() string
}

type ProcessResult struct {
	ExitCode int
	Stdout   []byte
	Stderr   []byte
}

// ProcessRunner executes external processes via argv only; implementations
// MUST NOT build or invoke a shell command string (ADD principle, Constitution §7).
type ProcessRunner interface {
	Run(ctx context.Context, dir string, name string, args ...string) (ProcessResult, error)
}
