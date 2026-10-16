package azurefunctions

import "fmt"

type AdapterError struct {
	Code    string
	Message string
	Cause   error
}

func (e *AdapterError) Error() string          { return e.description() }
func (e *AdapterError) Unwrap() error          { return e.Cause }
func (e *AdapterError) DiagnosticCode() string { return e.Code }

func (e *AdapterError) description() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Cause)
	}
	return e.Code + ": " + e.Message
}

func (e *AdapterError) DiagnosticMessage() string { return e.Message }
