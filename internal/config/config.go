// Package config loads Preflight's YAML configuration per the precedence
// chain fixed by Preflight_ADD.md §26.1:
//
//	CLI flags > environment > .preflight/local.yaml > .preflight/config.yaml
//	> global user config > defaults
//
// This package deliberately does NOT model every field in ADD §26.4's
// illustrative default configuration as a typed Go struct. As of this node,
// no other package in this repository consumes a single configuration
// field — runtime, predictor, policy, and checkpoint business logic are all
// out of scope for `foundation` (agents/foundation.md "Out of scope") and
// have not been implemented yet. Modeling the full ADD §26.4 tree now would
// invent fields nothing reads, which Constitution §7 rule 10 forbids
// ("does not add abstractions a later milestone would need but the current
// one doesn't").
//
// What this package DOES own, because the day-one vertical slice genuinely
// needs it: the schema_version envelope, layered YAML loading in the
// correct precedence order, unknown-field warn-vs-strict validation (ADD
// §26.2), and a documented merge/precedence algorithm every later role can
// build its own typed config section on top of via Config.Raw (a decoded
// map, not yet field-mapped into structs).
package config

import (
	"fmt"
	"os"

	"go.yaml.in/yaml/v3"
)

// SchemaVersion is the only schema_version value this package accepts
// (ADD §26.2). A config file declaring a different value is a validation
// error — Preflight has no migration story for config schema versions yet.
const SchemaVersion = "preflight.config.v1"

// UnknownFieldPolicy controls how Load handles YAML keys it does not
// recognize at the top level (ADD §26.2: "Unknown fields: default warn;
// strict validation error").
type UnknownFieldPolicy int

const (
	// WarnOnUnknownFields is the default: unknown top-level fields are
	// collected into Config.UnknownFields and surfaced to the caller,
	// but Load still succeeds.
	WarnOnUnknownFields UnknownFieldPolicy = iota
	// StrictUnknownFields makes any unrecognized top-level field a Load
	// error, per ADD §26.2's "strict validation" mode.
	StrictUnknownFields
)

// knownTopLevelFields are the only top-level keys this package currently
// recognizes as "known" for unknown-field detection purposes. This is
// intentionally just the envelope field plus the section names ADD §26.4
// documents, WITHOUT validating their internal shape — a later role adding
// a genuinely consumed section registers it here and owns its own typed
// decode from Config.Raw.
var knownTopLevelFields = map[string]bool{
	"schema_version":        true,
	"runtime":               true,
	"privacy":               true,
	"prediction":            true,
	"risk":                  true,
	"state_checkpointing":   true,
	"repository_checkpoint": true,
	"graceful_pause":        true,
}

// Source identifies where a layer in the precedence chain came from, for
// diagnostics (e.g. a future `preflight config show --effective`, owned by
// `runtime`, not this package).
type Source string

const (
	SourceDefaults    Source = "defaults"
	SourceGlobalUser  Source = "global_user_config"
	SourceRepoConfig  Source = "repo_config"
	SourceRepoLocal   Source = "repo_local"
	SourceEnvironment Source = "environment"
	SourceCLIFlags    Source = "cli_flags"
)

// precedenceOrder is lowest-to-highest priority, matching ADD §26.1 read
// top-to-bottom as highest-to-lowest; later entries here win merge
// conflicts.
var precedenceOrder = []Source{
	SourceDefaults,
	SourceGlobalUser,
	SourceRepoConfig,
	SourceRepoLocal,
	SourceEnvironment,
	SourceCLIFlags,
}

// Config is the loaded, merged configuration.
type Config struct {
	// SchemaVersion is the effective schema_version after merge. Always
	// equal to SchemaVersion on success (Load rejects any other value).
	SchemaVersion string
	// Raw is the fully merged configuration as a generic map, keyed by
	// top-level section name. Roles that need a section decode it
	// themselves (e.g. via yaml.Marshal(Raw["prediction"]) followed by
	// yaml.Unmarshal into their own typed struct) rather than this
	// package pre-declaring every section's shape.
	Raw map[string]any
	// UnknownFields lists top-level keys present in the merged input
	// that this package does not recognize (see knownTopLevelFields).
	// Only populated under WarnOnUnknownFields; StrictUnknownFields
	// turns the same condition into a Load error instead.
	UnknownFields []string
	// Layers records, in precedence order (lowest to highest), which
	// sources actually contributed a layer (i.e. were non-empty/present)
	// during this Load call. Useful for diagnostics.
	Layers []Source
}

// Layer is one input to Load: a named source and its raw YAML bytes.
// A Layer with no Bytes is treated as "this source had nothing to
// contribute" (e.g. an optional file that does not exist) and is skipped —
// callers decide whether a missing file is an error before constructing a
// Layer, this package only merges what it is given.
type Layer struct {
	Source Source
	Bytes  []byte
}

// Options controls Load behavior.
type Options struct {
	// UnknownFieldPolicy selects warn-vs-strict handling of unrecognized
	// top-level fields. Zero value is WarnOnUnknownFields.
	UnknownFieldPolicy UnknownFieldPolicy
}

// Load merges layers in Preflight's fixed precedence order (ADD §26.1),
// regardless of the order they are passed in — Load sorts by
// precedenceOrder internally, so callers may pass layers in any order.
// Passing the same Source twice is a caller error (ErrDuplicateSource).
//
// Merge semantics: a shallow, top-level-key merge. A higher-precedence
// layer's top-level key fully replaces a lower-precedence layer's value
// for that key (map values are not deep-merged within a section). This
// matches what every current consumer needs (none yet) and avoids
// prescribing a deep-merge algorithm no section's actual shape has been
// designed against; a future role needing section-level deep merge builds
// that on top of Raw once it owns a concrete section shape.
func Load(layers []Layer, opts Options) (Config, error) {
	bySource := make(map[Source][]byte, len(layers))
	for _, l := range layers {
		if _, dup := bySource[l.Source]; dup {
			return Config{}, fmt.Errorf("%w: %s", ErrDuplicateSource, l.Source)
		}
		bySource[l.Source] = l.Bytes
	}

	merged := map[string]any{}
	var usedLayers []Source

	for _, src := range precedenceOrder {
		raw, ok := bySource[src]
		if !ok || len(raw) == 0 {
			continue
		}

		var layerMap map[string]any
		if err := yaml.Unmarshal(raw, &layerMap); err != nil {
			return Config{}, fmt.Errorf("config: parsing %s layer: %w", src, err)
		}
		if layerMap == nil {
			continue
		}

		for k, v := range layerMap {
			merged[k] = v
		}
		usedLayers = append(usedLayers, src)
	}

	schemaVersion, _ := merged["schema_version"].(string)
	if schemaVersion == "" {
		return Config{}, fmt.Errorf("%w: schema_version is required", ErrInvalidSchemaVersion)
	}
	if schemaVersion != SchemaVersion {
		return Config{}, fmt.Errorf("%w: got %q, want %q", ErrInvalidSchemaVersion, schemaVersion, SchemaVersion)
	}

	var unknown []string
	for k := range merged {
		if !knownTopLevelFields[k] {
			unknown = append(unknown, k)
		}
	}

	if len(unknown) > 0 && opts.UnknownFieldPolicy == StrictUnknownFields {
		return Config{}, fmt.Errorf("%w: %v", ErrUnknownFields, unknown)
	}

	return Config{
		SchemaVersion: schemaVersion,
		Raw:           merged,
		UnknownFields: unknown,
		Layers:        usedLayers,
	}, nil
}

// LoadFile reads path and returns it as a Layer for the given source. A
// missing file is not an error — it returns a zero-value Layer (no bytes),
// which Load treats as "this source contributed nothing", matching that
// every layer below CLI flags/environment in ADD §26.1 is optional (global
// config, repo config, and repo local config may all be absent).
func LoadFile(source Source, path string) (Layer, error) {
	bytes, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Layer{Source: source}, nil
		}
		return Layer{}, fmt.Errorf("config: reading %s (%s): %w", source, path, err)
	}
	return Layer{Source: source, Bytes: bytes}, nil
}
