package api

type boundaryError struct {
	Code    string
	Message string
}

func (e *boundaryError) Error() string             { return e.Code + ": " + e.Message }
func (e *boundaryError) DiagnosticCode() string    { return e.Code }
func (e *boundaryError) DiagnosticMessage() string { return e.Message }

func (boundaryError) BadRequestCode() string  { return "HD0400" }
func (boundaryError) UnavailableCode() string { return "HD0503" }
