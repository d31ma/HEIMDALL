package provider

type DiagnosticError interface {
	error
	DiagnosticCode() string
	DiagnosticMessage() string
}
