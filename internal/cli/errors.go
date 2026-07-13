package cli

import (
	"encoding/json"
	"errors"

	"github.com/spf13/cobra"

	"github.com/huaiche94/preflight/internal/domain"
)

// notImplemented builds the frozen domain.Error shape (CONTRACT_FREEZE.md
// "Error contract") for a command whose underlying service does not exist
// yet. It uses ErrCodeUnavailable/Retryable: true — the command surface is
// real and will work once the corresponding service (orchestrator,
// evaluation, checkpoint, pause — internal/app/ports.go) is wired in a
// later node; this is an operational "not yet available," not a permanent
// validation failure or an integrity fault.
func notImplemented(command string) error {
	return &domain.Error{
		Code:      domain.ErrCodeUnavailable,
		Message:   "preflight " + command + ": not yet implemented",
		Retryable: true,
		Details: map[string]string{
			"command": command,
		},
	}
}

// SchemaVersionError is the frozen wire envelope every command's error path
// must emit (agents/runtime.md Part B "JSON and errors": "typed error
// code, message, retryable, details"; CONTRACT_FREEZE.md "Error contract").
// Distinct from any single command's own success-output schema-version
// string (e.g. "preflight.checkpoint-create.v1") — this ONE shared
// envelope covers every command's error path uniformly, since an error is
// not command-specific data, it is the same typed domain.Error shape
// regardless of which command produced it.
const SchemaVersionError = "preflight.error.v1"

// errorEnvelope is SchemaVersionError's wire shape: a schema-versioned
// wrapper around the frozen domain.Error fields, never a bare Go error
// string. json field names match CONTRACT_FREEZE.md's Error struct field
// order (Code, Message, Retryable, Details).
type errorEnvelope struct {
	SchemaVersion string            `json:"schema_version"`
	Code          domain.ErrorCode  `json:"code"`
	Message       string            `json:"message"`
	Retryable     bool              `json:"retryable"`
	Details       map[string]string `json:"details,omitempty"`
}

// RenderErrorJSON converts err into SchemaVersionError's wire shape. Any
// error is accepted, not just *domain.Error: a genuine composition bug (a
// nil dependency panic recovered upstream, a Cobra usage error, or any
// other error this package's own commands didn't originate) is rendered as
// ErrCodeInternal/Retryable:false rather than silently producing no JSON
// at all — CONTRACT_FREEZE.md's error contract is a hard gate on every
// command's error path, not just the ones this package remembered to wrap
// in *domain.Error.
func RenderErrorJSON(err error) []byte {
	var derr *domain.Error
	env := errorEnvelope{SchemaVersion: SchemaVersionError}
	if errors.As(err, &derr) {
		env.Code = derr.Code
		env.Message = derr.Message
		env.Retryable = derr.Retryable
		env.Details = derr.Details
	} else {
		env.Code = domain.ErrCodeInternal
		env.Message = err.Error()
		env.Retryable = false
	}
	body, marshalErr := json.Marshal(env)
	if marshalErr != nil {
		// Marshal can only fail here on a non-UTF8 Details value or
		// similar pathological input; fall back to a minimal, always-valid
		// envelope rather than emitting nothing (agents/runtime.md: "hook
		// fallback remains syntactically valid when Preflight fails" — the
		// same discipline applies to every command's error JSON, not just
		// hook responses).
		return []byte(`{"schema_version":"` + SchemaVersionError + `","code":"internal","message":"failed to encode error","retryable":false}`)
	}
	return body
}

// jsonErrorWrappedAnnotation marks a *cobra.Command whose RunE has already
// been wrapped by wrapCommandTree, so a second WithJSONErrorRendering pass
// (e.g. internal/app/wiring.App.RootCmd() re-applying it after
// replaceSubcommand swaps in fresh subtrees on top of cli.NewRootCmd()'s
// own already-wrapped tree) never double-wraps an untouched leaf and
// writes the JSON envelope twice. A freshly built replacement command
// (cli.NewHookClaudeCmd and friends) has no such annotation, so it is
// wrapped exactly once by whichever WithJSONErrorRendering call runs
// after it was attached to the tree.
const jsonErrorWrappedAnnotation = "preflight.jsonErrorWrapped"

// WithJSONErrorRendering walks root's entire command tree and wraps every
// not-yet-wrapped leaf's RunE so that, in addition to returning the
// original error unchanged (preserving every existing caller's
// errors.As(err, &domain.Error{}) behavior — see errors_test.go/
// root_test.go, neither of which this change may break), the SAME error is
// ALSO rendered as SchemaVersionError's JSON envelope and written to the
// command's own ErrOrStderr(). This closes runtime-b09's central finding:
// every command already constructs a *domain.Error internally, but
// nothing serialized that typed shape to JSON before this — Cobra's own
// default error printer (SilenceErrors: false) would otherwise flatten it
// to a bare ".Error()" plain-text line. The returned Go error is untouched
// specifically so this wrapping is purely additive: any test or caller
// that already inspects the returned error continues to work exactly as
// before; the JSON write is a new side effect on top, not a replacement.
// Safe to call more than once on the same tree (see
// jsonErrorWrappedAnnotation) — internal/app/wiring.App.RootCmd() relies
// on this to re-wrap only the subtrees it freshly replaces.
func WithJSONErrorRendering(root *cobra.Command) *cobra.Command {
	wrapCommandTree(root)
	return root
}

func wrapCommandTree(cmd *cobra.Command) {
	if cmd.RunE != nil && cmd.Annotations[jsonErrorWrappedAnnotation] == "" {
		original := cmd.RunE
		cmd.RunE = func(c *cobra.Command, args []string) error {
			err := original(c, args)
			if err != nil {
				body := RenderErrorJSON(err)
				_, _ = c.ErrOrStderr().Write(append(body, '\n'))
			}
			return err
		}
		if cmd.Annotations == nil {
			cmd.Annotations = map[string]string{}
		}
		cmd.Annotations[jsonErrorWrappedAnnotation] = "true"
	}
	for _, child := range cmd.Commands() {
		wrapCommandTree(child)
	}
}
