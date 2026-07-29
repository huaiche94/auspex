// Package hookinstall is the ownership-record hooks.json installer ADD
// §21.12 prescribes (issue #9 M8 slice): it merges Auspex's command
// hooks into a Claude-Code-shaped hooks.json WITHOUT disturbing user
// entries, records exactly what it added in a receipt (config path,
// previous content hash, the added entries, binary path, version,
// timestamp), and uninstalls by removing ONLY what the receipt owns.
//
// The config is edited as raw JSON maps so every unknown field a user or
// a future provider version put there survives round-trips untouched;
// writes are atomic (temp file + rename). Idempotency: an install that
// finds its command already present (whether from a prior install or
// hand-wiring) adds nothing and claims nothing — uninstall then leaves
// the hand-wired entry alone, exactly the §21.12 ownership rule.
package hookinstall

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ReceiptSchemaVersion stamps every receipt file.
const ReceiptSchemaVersion = "auspex.hook-install-receipt.v1"

// Entry is one command hook Auspex wires: the event it rides, the
// matcher group it creates, and the command line (the ownership key —
// uninstall matches on it verbatim).
type Entry struct {
	Event   string `json:"event"`
	Matcher string `json:"matcher"`
	Command string `json:"command"`
	Timeout int    `json:"timeout,omitempty"`
}

// Receipt is the §21.12 ownership record, stored OUTSIDE the provider's
// config (Auspex's own data dir) so a provider reinstall or config wipe
// never destroys the evidence of what Auspex added.
type Receipt struct {
	SchemaVersion  string  `json:"schema_version"`
	Provider       string  `json:"provider"`
	ConfigPath     string  `json:"config_path"`
	PreviousSHA256 string  `json:"previous_sha256"` // "" = config absent before install
	Entries        []Entry `json:"entries"`         // exactly what install ADDED (owned)
	BinaryPath     string  `json:"binary_path"`
	Version        string  `json:"version"`
	InstalledAt    string  `json:"installed_at"`
}

// InstallResult reports what happened, entry by entry.
type InstallResult struct {
	ConfigPath    string
	ReceiptPath   string
	Added         []Entry // newly wired this run (owned in the receipt)
	AlreadyWired  []Entry // present before this run (NOT owned)
	ConfigCreated bool
}

// UninstallResult mirrors the §21.12 removal contract.
type UninstallResult struct {
	ConfigPath  string
	ReceiptPath string
	Removed     []Entry // owned entries actually removed
	Missing     []Entry // owned entries that were already gone (user edit)
	NoReceipt   bool    // nothing owned: nothing touched
}

// Install merges entries into the hooks.json at configPath and writes
// the ownership receipt. provider/binaryPath/version/now are receipt
// metadata only.
func Install(configPath, receiptPath, provider, binaryPath, version string, entries []Entry, now time.Time) (InstallResult, error) {
	res := InstallResult{ConfigPath: configPath, ReceiptPath: receiptPath}

	raw, prevHash, existed, err := readConfig(configPath)
	if err != nil {
		return res, err
	}
	res.ConfigCreated = !existed

	hooks := ensureObject(raw, "hooks")
	var added []Entry
	for _, e := range entries {
		if eventContainsCommand(hooks, e.Event, e.Command) {
			res.AlreadyWired = append(res.AlreadyWired, e)
			continue
		}
		appendEntry(hooks, e)
		added = append(added, e)
	}
	res.Added = added

	if len(added) > 0 || !existed {
		if err := writeJSONAtomic(configPath, raw); err != nil {
			return res, err
		}
	}

	receipt := Receipt{
		SchemaVersion:  ReceiptSchemaVersion,
		Provider:       provider,
		ConfigPath:     configPath,
		PreviousSHA256: prevHash,
		Entries:        added,
		BinaryPath:     binaryPath,
		Version:        version,
		InstalledAt:    now.UTC().Format(time.RFC3339),
	}
	if prior, ok := readReceipt(receiptPath); ok {
		// Re-install on top of a prior install: ownership accumulates
		// (entries the prior receipt owns stay owned; hand-wired ones
		// stay unowned) — never lose the older claim.
		receipt.Entries = mergeEntries(prior.Entries, added)
		if prior.PreviousSHA256 != "" || prior.ConfigPath == configPath {
			receipt.PreviousSHA256 = prior.PreviousSHA256
		}
	}
	if err := writeJSONAtomic(receiptPath, receipt); err != nil {
		return res, err
	}
	return res, nil
}

// Uninstall removes exactly the receipt-owned entries from the config
// and deletes the receipt. A missing receipt removes nothing (§21.12:
// no ownership record, no touch).
func Uninstall(configPath, receiptPath string) (UninstallResult, error) {
	res := UninstallResult{ConfigPath: configPath, ReceiptPath: receiptPath}

	receipt, ok := readReceipt(receiptPath)
	if !ok {
		res.NoReceipt = true
		return res, nil
	}

	raw, _, existed, err := readConfig(configPath)
	if err != nil {
		return res, err
	}
	if existed {
		hooks := ensureObject(raw, "hooks")
		for _, e := range receipt.Entries {
			if removeCommand(hooks, e.Event, e.Command) {
				res.Removed = append(res.Removed, e)
			} else {
				res.Missing = append(res.Missing, e)
			}
		}
		if len(raw) > 0 {
			if err := writeJSONAtomic(configPath, raw); err != nil {
				return res, err
			}
		}
	} else {
		res.Missing = receipt.Entries
	}

	if err := os.Remove(receiptPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return res, fmt.Errorf("hookinstall: remove receipt: %w", err)
	}
	return res, nil
}

// InstalledEntries reports which of entries are currently wired in the
// config — the doctor's hook-installed check.
func InstalledEntries(configPath string, entries []Entry) (present, absent []Entry, err error) {
	raw, _, existed, err := readConfig(configPath)
	if err != nil {
		return nil, nil, err
	}
	if !existed {
		return nil, entries, nil
	}
	hooks := ensureObject(raw, "hooks")
	for _, e := range entries {
		if eventContainsCommand(hooks, e.Event, e.Command) {
			present = append(present, e)
		} else {
			absent = append(absent, e)
		}
	}
	return present, absent, nil
}

// --- config surgery over raw JSON maps ----------------------------------

func readConfig(path string) (raw map[string]any, sha string, existed bool, err error) {
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]any{}, "", false, nil
	}
	if err != nil {
		return nil, "", false, fmt.Errorf("hookinstall: read %s: %w", path, err)
	}
	sum := sha256.Sum256(body)
	raw = map[string]any{}
	if len(body) > 0 {
		if err := json.Unmarshal(body, &raw); err != nil {
			// A config Auspex cannot parse is a config Auspex must not
			// rewrite — fail closed, never clobber (§21.12's spirit).
			return nil, "", false, fmt.Errorf("hookinstall: %s is not valid JSON (refusing to modify it): %w", path, err)
		}
	}
	return raw, hex.EncodeToString(sum[:]), true, nil
}

func ensureObject(m map[string]any, key string) map[string]any {
	if obj, ok := m[key].(map[string]any); ok {
		return obj
	}
	obj := map[string]any{}
	m[key] = obj
	return obj
}

// eventContainsCommand scans every matcher group of an event for a
// command hook with the exact command string.
func eventContainsCommand(hooks map[string]any, event, command string) bool {
	groups, _ := hooks[event].([]any)
	for _, g := range groups {
		group, _ := g.(map[string]any)
		inner, _ := group["hooks"].([]any)
		for _, h := range inner {
			hook, _ := h.(map[string]any)
			if hook["command"] == command {
				return true
			}
		}
	}
	return false
}

// appendEntry adds a fresh matcher group carrying exactly our command —
// never injected into a user's existing group, so uninstall can remove
// the group without touching neighbors.
func appendEntry(hooks map[string]any, e Entry) {
	hook := map[string]any{"type": "command", "command": e.Command}
	if e.Timeout > 0 {
		hook["timeout"] = e.Timeout
	}
	group := map[string]any{"matcher": e.Matcher, "hooks": []any{hook}}
	groups, _ := hooks[e.Event].([]any)
	hooks[e.Event] = append(groups, group)
}

// removeCommand deletes our command from an event's groups, dropping a
// group (and the event key) only when OUR removal emptied it.
func removeCommand(hooks map[string]any, event, command string) bool {
	groups, _ := hooks[event].([]any)
	removed := false
	var keptGroups []any
	for _, g := range groups {
		group, _ := g.(map[string]any)
		inner, _ := group["hooks"].([]any)
		var keptHooks []any
		for _, h := range inner {
			hook, _ := h.(map[string]any)
			if hook["command"] == command {
				removed = true
				continue
			}
			keptHooks = append(keptHooks, h)
		}
		if len(keptHooks) == 0 && removed && len(inner) > 0 {
			continue // the group held only our hook — drop it
		}
		group["hooks"] = keptHooks
		keptGroups = append(keptGroups, group)
	}
	if removed {
		if len(keptGroups) == 0 {
			delete(hooks, event)
		} else {
			hooks[event] = keptGroups
		}
	}
	return removed
}

func readReceipt(path string) (Receipt, bool) {
	body, err := os.ReadFile(path)
	if err != nil {
		return Receipt{}, false
	}
	var r Receipt
	if json.Unmarshal(body, &r) != nil || r.SchemaVersion != ReceiptSchemaVersion {
		return Receipt{}, false
	}
	return r, true
}

func mergeEntries(prior, added []Entry) []Entry {
	seen := map[string]bool{}
	var out []Entry
	for _, e := range append(append([]Entry{}, prior...), added...) {
		key := e.Event + "\x00" + e.Command
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, e)
	}
	return out
}

func writeJSONAtomic(path string, v any) error {
	body, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("hookinstall: encode %s: %w", path, err)
	}
	body = append(body, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("hookinstall: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".auspex-hookinstall-*")
	if err != nil {
		return fmt.Errorf("hookinstall: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("hookinstall: write %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("hookinstall: close temp for %s: %w", path, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("hookinstall: rename into %s: %w", path, err)
	}
	return nil
}

// CodexEntries are the three command hooks integrations/codex/hooks.json
// documents — the single source the installer AND the doctor check use.
func CodexEntries() []Entry {
	return []Entry{
		{Event: "SessionStart", Matcher: "", Command: "auspex hook codex session-start", Timeout: 5},
		{Event: "UserPromptSubmit", Matcher: "", Command: "auspex hook codex user-prompt-submit", Timeout: 5},
		{Event: "Stop", Matcher: "", Command: "auspex hook codex stop", Timeout: 5},
	}
}
