package spec

import (
	"errors"
)

type WireSchema struct {
	Spec DeploySpec
}

func (w WireSchema) Validate() error {
	return errors.Join(w.versionError(), w.functionErrors())
}

func (w WireSchema) versionError() error {
	if w.Spec.SchemaVersion == SchemaVersion || w.Spec.SchemaVersion == (Function{}).WireVersion() {
		return nil
	}
	return &WireSchemaError{Version: w.Spec.SchemaVersion}
}

func (w WireSchema) functionErrors() error {
	var failures []error
	for _, service := range w.Spec.Services {
		failures = append(failures, w.serviceError(service))
	}
	return errors.Join(failures...)
}

func (w WireSchema) serviceError(service Service) error {
	if service.Function == nil {
		return nil
	}
	return service.Function.Validate()
}
