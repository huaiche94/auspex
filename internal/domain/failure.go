package domain

type FailureClass string

const (
	FailureQuota             FailureClass = "quota"
	FailureContext           FailureClass = "context"
	FailureProviderRateLimit FailureClass = "provider_rate_limit"
	FailureProviderInternal  FailureClass = "provider_internal"
	FailureTool              FailureClass = "tool"
	FailureBuild             FailureClass = "build"
	FailureTest              FailureClass = "test"
	FailurePermission        FailureClass = "permission"
	FailureUserInterrupt     FailureClass = "user_interrupt"
	FailureNetwork           FailureClass = "network"
	FailureTimeout           FailureClass = "timeout"
	FailurePolicy            FailureClass = "policy"
	FailureCheckpoint        FailureClass = "checkpoint"
	FailureStateCheckpoint   FailureClass = "state_checkpoint"
	FailureResumeConflict    FailureClass = "resume_conflict"
	FailureResumeAuth        FailureClass = "resume_auth"
	FailureUnknown           FailureClass = "unknown"
)
