package lambda

import "fmt"

type Diagnostic struct {
	Code    string
	Message string
}

func (d Diagnostic) Format(code, message string, values ...any) *Diagnostic {
	return &Diagnostic{Code: code, Message: fmt.Sprintf(message, values...)}
}

func (d *Diagnostic) Error() string          { return d.Code + ": " + d.Message }
func (d *Diagnostic) DiagnosticCode() string { return d.Code }

func (Diagnostic) TargetCode() string { return "HD0410" }

func (Diagnostic) CredentialCode() string { return "HD0411" }

func (Diagnostic) InventoryCode() string { return "HD0412" }

func (Diagnostic) ApplyCode() string { return "HD0413" }

func (Diagnostic) OwnershipCode() string { return "HD0414" }

func (Diagnostic) ReadinessCode() string { return "HD0415" }

func (Diagnostic) EnvironmentCode() string { return "HD0416" }

func (Diagnostic) ObservationCode() string { return "HD0417" }

func (Diagnostic) InvocationCode() string { return "HD0418" }

func (Diagnostic) ResponseLimitCode() string { return "HD0419" }
