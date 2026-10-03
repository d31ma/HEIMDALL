package azurefunctions

import (
	"github.com/d31ma/heimdall/internal/provider"
	"github.com/d31ma/heimdall/internal/sequence"
)

type targetConfig struct {
	subscription string
	group        string
	environment  string
	identity     string
	clientID     string
	storageName  string
	region       string
}

func (targetConfig) parse(target provider.Target) (targetConfig, error) {
	validation := targetValidation{target: target}
	err := (sequence.Steps{Actions: []func() error{validation.keys, validation.required, validation.access, validation.environment, validation.identity, validation.storage, validation.storageName, validation.operationID}}).Run()
	return validation.result(err)
}

func (targetConfig) validateDefinition(target provider.Target) error {
	validation := targetValidation{target: target}
	return (sequence.Steps{Actions: []func() error{validation.keys, validation.required, validation.access, validation.environment, validation.identity, validation.storage, validation.storageName}}).Run()
}

func (targetConfig) validateKeys(config map[string]string) error {
	return (targetKeys{}).validate(config)
}
