package cli

import "github.com/huaiche94/preflight/internal/domain"

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
