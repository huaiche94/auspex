package domain

type ErrorCode string

const (
	ErrCodeValidation   ErrorCode = "validation"
	ErrCodeNotFound     ErrorCode = "not_found"
	ErrCodeConflict     ErrorCode = "conflict"
	ErrCodeUnauthorized ErrorCode = "unauthorized"
	ErrCodeIntegrity    ErrorCode = "integrity"
	ErrCodeUnavailable  ErrorCode = "unavailable"
	ErrCodeInternal     ErrorCode = "internal"
)

type Error struct {
	Code      ErrorCode
	Message   string
	Retryable bool
	Details   map[string]string
}

func (e *Error) Error() string {
	return string(e.Code) + ": " + e.Message
}
