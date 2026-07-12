package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/huaiche94/preflight/internal/buildinfo"
)

func TestVersionCommandPrintsVersionString(t *testing.T) {
	root := newRootCmd()

	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"version"})

	if err := root.Execute(); err != nil {
		t.Fatalf("execute `preflight version`: %v", err)
	}

	got := strings.TrimSpace(out.String())
	if got != buildinfo.String() {
		t.Fatalf("version command printed %q, want %q", got, buildinfo.String())
	}
	if got == "" {
		t.Fatal("version command printed an empty string")
	}
}

func TestRootCommandHasVersionSubcommand(t *testing.T) {
	root := newRootCmd()

	cmd, _, err := root.Find([]string{"version"})
	if err != nil {
		t.Fatalf("find version subcommand: %v", err)
	}
	if cmd.Name() != "version" {
		t.Fatalf("found command %q, want %q", cmd.Name(), "version")
	}
}
