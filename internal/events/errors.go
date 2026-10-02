package events

import "fmt"

// PermanentError indicates a non-retryable failure (e.g. invalid JSON, missing critical fields).
// Such errors bypass retry tiers and route immediately to the DLQ.
type PermanentError struct {
	Err error
}

func (e PermanentError) Error() string {
	return fmt.Sprintf("permanent error: %v", e.Err)
}

func (e PermanentError) Unwrap() error {
	return e.Err
}

// NewPermanentError constructs a PermanentError with formatted message.
func NewPermanentError(format string, args ...any) PermanentError {
	return PermanentError{Err: fmt.Errorf(format, args...)}
}

// TransientError indicates a retryable failure (e.g. DB connection lost, temporary network blip).
type TransientError struct {
	Err error
}

func (e TransientError) Error() string {
	return fmt.Sprintf("transient error: %v", e.Err)
}

func (e TransientError) Unwrap() error {
	return e.Err
}

// NewTransientError constructs a TransientError with formatted message.
func NewTransientError(format string, args ...any) TransientError {
	return TransientError{Err: fmt.Errorf(format, args...)}
}
