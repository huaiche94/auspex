// Command fakeprovider is the test stand-in for the real `claude` CLI in
// internal/managed's and internal/integrationtest's managed-run tests.
// It lives under testdata/ so the go tool never builds it as part of
// ./... — tests compile it on demand with `go build` into a temp
// directory (a compiled Go helper, NOT a shell script, so the same test
// binary works on windows-latest CI where chmod +x shell fakes do not).
//
// Behavior is driven entirely by environment variables the spawning test
// sets (the managed runner inherits the test process's environment, the
// default os/exec behavior):
//
//	AUSPEX_FAKE_ARGV_FILE   when set, the received argv (excluding
//	                        argv[0]) is written to this path as a JSON
//	                        array, so tests can assert the runner passed
//	                        exactly the argv-only invocation it promises
//	                        (Constitution §7 rule 5) — and, on the BLOCK
//	                        path, that this file was never created at all
//	                        (the provider was never spawned).
//	AUSPEX_FAKE_STREAM_FILE when set, this file's bytes are copied
//	                        verbatim to stdout — the canned stream-json
//	                        session (fixtures documented in
//	                        stream_test.go).
//	AUSPEX_FAKE_STDERR      when set, this literal string is written to
//	                        stderr (exercises the runner's stderr
//	                        passthrough).
//	AUSPEX_FAKE_EXIT_CODE   process exit code (default 0).
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
)

func main() {
	if path := os.Getenv("AUSPEX_FAKE_ARGV_FILE"); path != "" {
		body, err := json.Marshal(os.Args[1:])
		if err == nil {
			err = os.WriteFile(path, body, 0o644)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "fakeprovider: writing argv file:", err)
			os.Exit(3)
		}
	}
	if path := os.Getenv("AUSPEX_FAKE_STREAM_FILE"); path != "" {
		body, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, "fakeprovider: reading stream file:", err)
			os.Exit(3)
		}
		if _, err := os.Stdout.Write(body); err != nil {
			os.Exit(3)
		}
	}
	if msg := os.Getenv("AUSPEX_FAKE_STDERR"); msg != "" {
		fmt.Fprintln(os.Stderr, msg)
	}
	code := 0
	if v := os.Getenv("AUSPEX_FAKE_EXIT_CODE"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			code = n
		}
	}
	os.Exit(code)
}
