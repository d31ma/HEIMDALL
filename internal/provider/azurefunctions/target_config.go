package azurefunctions

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/d31ma/heimdall/internal/provider"
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
	if err := (targetConfig{}).validateKeys(target.Config); err != nil {
		return targetConfig{}, err
	}
	config := targetConfig{subscription: target.Config["subscription_id"], group: target.Config["resource_group"], environment: target.Endpoint, identity: target.Config["identity_resource_id"], clientID: target.Config["identity_client_id"], region: target.Region}
	guid := regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	group := regexp.MustCompile(`^[a-zA-Z0-9_.()-]{1,90}$`)
	if !guid.MatchString(config.subscription) || !guid.MatchString(config.clientID) || !group.MatchString(config.group) || strings.HasSuffix(config.group, ".") || target.ID == "" || target.Region == "" || target.Provider != "azurefunctions" {
		return targetConfig{}, (&AdapterError{Code: "HD0470", Message: "azurefunctions requires a target ID, region, subscription UUID, resource group and identity client UUID"})
	}
	if target.CredentialRef != "" || target.AgentID != "" {
		return targetConfig{}, (&AdapterError{Code: "HD0470", Message: "azurefunctions initially uses the ambient ARM credential chain and direct provider access"})
	}
	if _, err := (resourceIdentity{}).reference(config.environment, config.subscription, "Microsoft.App/managedEnvironments"); err != nil {
		return targetConfig{}, err
	}
	if _, err := (resourceIdentity{}).reference(config.identity, config.subscription, "Microsoft.ManagedIdentity/userAssignedIdentities"); err != nil {
		return targetConfig{}, err
	}
	storage, err := (resourceIdentity{}).reference(target.Config["storage_account_resource_id"], config.subscription, "Microsoft.Storage/storageAccounts")
	if err != nil {
		return targetConfig{}, err
	}
	validStorage := regexp.MustCompile(`^[a-z0-9]{3,24}$`)
	if !validStorage.MatchString(storage) {
		return targetConfig{}, (&AdapterError{Code: "HD0470", Message: "host storage must reference a valid existing Azure storage account"})
	}
	config.storageName = storage
	return config, nil
}

func (targetConfig) validateKeys(config map[string]string) error {
	unsupported := []string{}
	for key := range config {
		switch key {
		case "subscription_id", "resource_group", "storage_account_resource_id", "identity_resource_id", "identity_client_id":
		default:
			unsupported = append(unsupported, key)
		}
	}
	if len(unsupported) == 0 {
		return nil
	}
	sort.Strings(unsupported)
	return &AdapterError{Code: "HD0470", Message: fmt.Sprintf("unsupported azurefunctions target configuration keys %q; supported keys are subscription_id, resource_group, storage_account_resource_id, identity_resource_id and identity_client_id", unsupported)}
}
