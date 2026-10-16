package azurefunctions

import "time"

type functionContract struct{}

func (functionContract) providerName() string    { return "azurefunctions" }
func (functionContract) resourceKind() string    { return "functionapp" }
func (functionContract) apiVersion() string      { return "2026-07-01" }
func (functionContract) environmentType() string { return "Microsoft.App/managedEnvironments" }
func (functionContract) identityType() string {
	return "Microsoft.ManagedIdentity/userAssignedIdentities"
}
func (functionContract) storageType() string               { return "Microsoft.Storage/storageAccounts" }
func (functionContract) runtime() string                   { return "azure-functions-v4" }
func (functionContract) invocation() string                { return "http" }
func (functionContract) architecture() string              { return "amd64" }
func (functionContract) extensionSetting() string          { return "FUNCTIONS_EXTENSION_VERSION" }
func (functionContract) storagePrefix() string             { return "azurewebjobsstorage" }
func (functionContract) targetPort() int32                 { return 80 }
func (functionContract) maximumReplicas() int32            { return 10 }
func (functionContract) responseBytes() int64              { return 1048576 }
func (functionContract) maximumPages() int                 { return 100 }
func (functionContract) maximumResources() int             { return 10000 }
func (functionContract) readTimeout() time.Duration        { return 30 * time.Second }
func (functionContract) convergenceTimeout() time.Duration { return 5 * time.Minute }
func (functionContract) pollDelay() time.Duration          { return 250 * time.Millisecond }
func (functionContract) defaultProject() string            { return "default" }
func (functionContract) separator() string                 { return "\x00" }
func (functionContract) subscriptionKey() string           { return "subscription_id" }
func (functionContract) groupKey() string                  { return "resource_group" }
func (functionContract) storageKey() string                { return "storage_account_resource_id" }
func (functionContract) identityKey() string               { return "identity_resource_id" }
func (functionContract) clientKey() string                 { return "identity_client_id" }
func (functionContract) configurationCode() string         { return "HD0470" }
func (functionContract) clientCode() string                { return "HD0471" }
func (functionContract) readCode() string                  { return "HD0472" }
func (functionContract) responseCode() string              { return "HD0473" }
func (functionContract) ownershipCode() string             { return "HD0474" }
func (functionContract) planCode() string                  { return "HD0475" }
func (functionContract) convergenceCode() string           { return "HD0476" }

func (functionContract) pathSeparator() string   { return "/" }
func (functionContract) observationCode() string { return "HD0477" }

func (functionContract) containerCount() int   { return 1 }
func (functionContract) declaredReplicas() int { return 1 }
