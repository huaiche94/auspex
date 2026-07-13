package paths_test

import "github.com/huaiche94/auspex/internal/paths"

// fakeEnv is a deterministic, in-memory Env for tests. It never touches the
// real process environment or home directory, so path-table tests can
// exercise every OS branch (including Windows) from any host OS.
type fakeEnv struct {
	vars map[string]string
	home string
	// homeErr, if non-nil, is returned by UserHomeDir instead of home.
	homeErr error
}

func newFakeEnv(home string) *fakeEnv {
	return &fakeEnv{vars: map[string]string{}, home: home}
}

func (f *fakeEnv) with(key, val string) *fakeEnv {
	f.vars[key] = val
	return f
}

func (f *fakeEnv) Getenv(key string) string {
	return f.vars[key]
}

func (f *fakeEnv) UserHomeDir() (string, error) {
	if f.homeErr != nil {
		return "", f.homeErr
	}
	return f.home, nil
}

var _ paths.Env = (*fakeEnv)(nil)
