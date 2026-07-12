package paths

import "os"

// OSEnv is the real Env implementation backed by the process environment
// and os.UserHomeDir. Production code uses this; tests use a fake Env so
// path resolution can be exercised without mutating real process state.
type OSEnv struct{}

// NewOSEnv returns an Env backed by the real OS environment.
func NewOSEnv() Env {
	return OSEnv{}
}

// Getenv implements Env.
func (OSEnv) Getenv(key string) string {
	return os.Getenv(key)
}

// UserHomeDir implements Env.
func (OSEnv) UserHomeDir() (string, error) {
	return os.UserHomeDir()
}
