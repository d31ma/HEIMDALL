package spec

import "fmt"

type FunctionContractError struct {
	Field   string
	Message string
}

func (e *FunctionContractError) Error() string {
	return fmt.Sprintf("HD0223: x-heimdall-function.%s: %s", e.Field, e.Message)
}
