package domain

type ProviderCapabilities struct {
	PrePromptGate           bool
	HookAdditionalContext   bool
	ManagedExecution        bool
	StructuredEventStream   bool
	ExactTurnUsage          bool
	LiveTokenUsage          bool
	ContextWindowUsage      bool
	RollingQuotaUsage       bool
	QuotaResetTimestamp     bool
	PlanEvents              bool
	TaskEvents              bool
	FileChangeEvents        bool
	ToolEvents              bool
	SafePointControl        bool
	TurnInterrupt           bool
	SessionResume           bool
	SessionFork             bool
	NativeStatusLine        bool
	NativeInteractiveChoice bool
}
