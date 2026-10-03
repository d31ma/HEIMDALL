package spec

import "fmt"

type WireSchemaError struct {
	Version string
}

func (e *WireSchemaError) Error() string {
	return fmt.Sprintf("HD0224: unsupported DeploySpec schema %q; use the matching control-plane version", e.Version)
}
