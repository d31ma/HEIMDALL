package azurefunctions

import (
	"regexp"
	"strings"

	"github.com/d31ma/heimdall/app/server/services/provider"
)

type targetValidation struct {
	target provider.Target
	config targetConfig
}

func (v *targetValidation) keys() error { return (targetKeys{}).validate(v.target.Config) }
func (v *targetValidation) required() error {
	contract := functionContract{}
	v.config = targetConfig{subscription: v.target.Config[contract.subscriptionKey()], group: v.target.Config[contract.groupKey()], environment: v.target.Endpoint, identity: v.target.Config[contract.identityKey()], clientID: v.target.Config[contract.clientKey()], region: v.target.Region}
	guid := regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	group := regexp.MustCompile((resourceReference{}).namePattern())
	if !guid.MatchString(v.config.subscription) || !guid.MatchString(v.config.clientID) || !group.MatchString(v.config.group) || strings.HasSuffix(v.config.group, ".") || v.target.Region == "" || v.target.Provider != contract.providerName() {
		return &AdapterError{Code: contract.configurationCode(), Message: "azurefunctions requires a region, subscription UUID, resource group and identity client UUID"}
	}
	return nil
}
func (v *targetValidation) operationID() error {
	if v.target.ID == "" {
		return &AdapterError{Code: (functionContract{}).configurationCode(), Message: "azurefunctions operations require a target ID"}
	}
	return nil
}
func (v *targetValidation) access() error {
	if v.target.CredentialRef != "" || v.target.AgentID != "" {
		return &AdapterError{Code: (functionContract{}).configurationCode(), Message: "azurefunctions initially uses the ambient ARM credential chain and direct provider access"}
	}
	return nil
}
func (v *targetValidation) environment() error {
	_, err := (resourceIdentity{}).reference(v.config.environment, v.config.subscription, (functionContract{}).environmentType())
	return err
}
func (v *targetValidation) identity() error {
	_, err := (resourceIdentity{}).reference(v.config.identity, v.config.subscription, (functionContract{}).identityType())
	return err
}
func (v *targetValidation) storage() error {
	name, err := (resourceIdentity{}).reference(v.target.Config[(functionContract{}).storageKey()], v.config.subscription, (functionContract{}).storageType())
	v.config.storageName = name
	return err
}
func (v *targetValidation) storageName() error {
	if !regexp.MustCompile(`^[a-z0-9]{3,24}$`).MatchString(v.config.storageName) {
		return &AdapterError{Code: (functionContract{}).configurationCode(), Message: "host storage must reference a valid existing Azure storage account"}
	}
	return nil
}
func (v *targetValidation) result(err error) (targetConfig, error) {
	if err != nil {
		return targetConfig{}, err
	}
	return v.config, nil
}
