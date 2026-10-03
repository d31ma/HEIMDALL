package reconcile

type InvocationError struct {
	Code    string
	Message string
}

func (e *InvocationError) Error() string             { return e.Code + ": " + e.Message }
func (e *InvocationError) DiagnosticCode() string    { return e.Code }
func (e *InvocationError) DiagnosticMessage() string { return e.Message }
func (InvocationError) SuspendedCode() string        { return "HD0280" }
func (InvocationError) ServiceCode() string          { return "HD0281" }
func (InvocationError) UnsupportedCode() string      { return "HD0282" }

func (InvocationError) ObservationCode() string { return "HD0283" }
func (InvocationError) VersionCode() string     { return "HD0284" }
