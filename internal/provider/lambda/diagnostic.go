package lambda

import "fmt"

type Diagnostic struct {
	Message string
}

func (d Diagnostic) Format(format string, values ...any) *Diagnostic {
	return &Diagnostic{Message: fmt.Sprintf(format, values...)}
}

func (d *Diagnostic) Error() string { return d.Message }
