package hookinstall

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var testNow = time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)

func paths(t *testing.T) (config, receipt string) {
	t.Helper()
	dir := t.TempDir()
	return filepath.Join(dir, "hooks.json"), filepath.Join(dir, "receipt.json")
}

func install(t *testing.T, config, receipt string) InstallResult {
	t.Helper()
	res, err := Install(config, receipt, "codex", "/usr/local/bin/codex", "0.144.5", CodexEntries(), testNow)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	return res
}

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return m
}

func TestInstall_FreshConfig(t *testing.T) {
	config, receipt := paths(t)
	res := install(t, config, receipt)

	if !res.ConfigCreated || len(res.Added) != 3 || len(res.AlreadyWired) != 0 {
		t.Fatalf("res = %+v", res)
	}
	m := readJSON(t, config)
	hooks := m["hooks"].(map[string]any)
	for _, ev := range []string{"SessionStart", "UserPromptSubmit", "Stop"} {
		if _, ok := hooks[ev]; !ok {
			t.Errorf("event %s missing from installed config", ev)
		}
	}
	var r Receipt
	body, _ := os.ReadFile(receipt)
	if err := json.Unmarshal(body, &r); err != nil {
		t.Fatalf("receipt: %v", err)
	}
	if r.SchemaVersion != ReceiptSchemaVersion || len(r.Entries) != 3 ||
		r.PreviousSHA256 != "" || r.Provider != "codex" || r.Version != "0.144.5" {
		t.Errorf("receipt = %+v", r)
	}
}

func TestInstall_PreservesUserEntriesAndUnknownFields(t *testing.T) {
	config, receipt := paths(t)
	existing := `{
  "unknown_top_level": {"keep": true},
  "hooks": {
    "SessionStart": [
      {"matcher": "", "hooks": [{"type": "command", "command": "my-own-hook.sh", "custom_field": 7}]}
    ]
  }
}`
	if err := os.WriteFile(config, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	res := install(t, config, receipt)
	if res.ConfigCreated || len(res.Added) != 3 {
		t.Fatalf("res = %+v", res)
	}

	m := readJSON(t, config)
	if _, ok := m["unknown_top_level"]; !ok {
		t.Error("unknown top-level field lost")
	}
	groups := m["hooks"].(map[string]any)["SessionStart"].([]any)
	if len(groups) != 2 {
		t.Fatalf("SessionStart groups = %d, want user group + auspex group", len(groups))
	}
	userHook := groups[0].(map[string]any)["hooks"].([]any)[0].(map[string]any)
	if userHook["command"] != "my-own-hook.sh" || userHook["custom_field"] != float64(7) {
		t.Errorf("user hook mutated: %+v", userHook)
	}
}

func TestInstall_Idempotent(t *testing.T) {
	config, receipt := paths(t)
	install(t, config, receipt)
	res2 := install(t, config, receipt)

	if len(res2.Added) != 0 || len(res2.AlreadyWired) != 3 {
		t.Fatalf("second install res = %+v", res2)
	}
	// Ownership survives the re-install (receipt merge).
	body, _ := os.ReadFile(receipt)
	var r Receipt
	_ = json.Unmarshal(body, &r)
	if len(r.Entries) != 3 {
		t.Errorf("receipt entries = %d after re-install, want 3 still owned", len(r.Entries))
	}
	groups := readJSON(t, config)["hooks"].(map[string]any)["SessionStart"].([]any)
	if len(groups) != 1 {
		t.Errorf("SessionStart groups = %d after re-install, want no duplicates", len(groups))
	}
}

func TestInstall_HandWiredCommandIsNotClaimed(t *testing.T) {
	config, receipt := paths(t)
	existing := `{"hooks":{"Stop":[{"matcher":"","hooks":[{"type":"command","command":"auspex hook codex stop","timeout":9}]}]}}`
	if err := os.WriteFile(config, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	res := install(t, config, receipt)
	if len(res.Added) != 2 || len(res.AlreadyWired) != 1 {
		t.Fatalf("res = %+v, want the hand-wired stop hook left unclaimed", res)
	}

	// Uninstall must leave the hand-wired entry in place (§21.12).
	un, err := Uninstall(config, receipt)
	if err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if len(un.Removed) != 2 {
		t.Errorf("removed = %+v, want exactly the 2 owned entries", un.Removed)
	}
	m := readJSON(t, config)
	if !strings.Contains(mustJSON(t, m), "auspex hook codex stop") {
		t.Error("hand-wired stop hook was removed although never owned")
	}
}

func TestUninstall_RemovesOnlyOwned_DropsEmptyEvents(t *testing.T) {
	config, receipt := paths(t)
	install(t, config, receipt)

	un, err := Uninstall(config, receipt)
	if err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if len(un.Removed) != 3 || len(un.Missing) != 0 || un.NoReceipt {
		t.Fatalf("un = %+v", un)
	}
	hooks := readJSON(t, config)["hooks"].(map[string]any)
	if len(hooks) != 0 {
		t.Errorf("hooks = %+v, want every auspex-only event key dropped", hooks)
	}
	if _, err := os.Stat(receipt); !os.IsNotExist(err) {
		t.Error("receipt should be deleted after uninstall")
	}
}

func TestUninstall_NoReceiptTouchesNothing(t *testing.T) {
	config, receipt := paths(t)
	existing := `{"hooks":{"Stop":[{"matcher":"","hooks":[{"type":"command","command":"auspex hook codex stop"}]}]}}`
	if err := os.WriteFile(config, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	un, err := Uninstall(config, receipt)
	if err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if !un.NoReceipt || len(un.Removed) != 0 {
		t.Fatalf("un = %+v", un)
	}
	if body, _ := os.ReadFile(config); string(body) != existing {
		t.Error("config modified without an ownership record")
	}
}

func TestInstall_CorruptConfigFailsClosed(t *testing.T) {
	config, receipt := paths(t)
	if err := os.WriteFile(config, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Install(config, receipt, "codex", "", "", CodexEntries(), testNow)
	if err == nil {
		t.Fatal("Install: want a refusal on unparseable config, never a clobber")
	}
	if body, _ := os.ReadFile(config); string(body) != "{not json" {
		t.Error("corrupt config was rewritten")
	}
}

func TestInstalledEntries(t *testing.T) {
	config, receipt := paths(t)
	present, absent, err := InstalledEntries(config, CodexEntries())
	if err != nil || len(present) != 0 || len(absent) != 3 {
		t.Fatalf("pre-install: present=%d absent=%d err=%v", len(present), len(absent), err)
	}
	install(t, config, receipt)
	present, absent, err = InstalledEntries(config, CodexEntries())
	if err != nil || len(present) != 3 || len(absent) != 0 {
		t.Fatalf("post-install: present=%d absent=%d err=%v", len(present), len(absent), err)
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
