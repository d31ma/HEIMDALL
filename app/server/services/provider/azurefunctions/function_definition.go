package azurefunctions

import (
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers/v3"
	"github.com/d31ma/heimdall/app/server/models/spec"
	"github.com/d31ma/heimdall/app/server/services/provider"
	"github.com/d31ma/heimdall/app/server/utils/sequence"
)

type functionDefinition struct {
	config  targetConfig
	plan    provider.Plan
	service spec.Service
	hash    string
	value   nativeApp
}

func (d functionDefinition) build(plan provider.Plan, service spec.Service) (nativeApp, error) {
	d.plan, d.service = plan, service
	err := (sequence.Steps{Actions: []func() error{d.hashService, d.define}}).Run()
	return d.result(err)
}
func (d *functionDefinition) hashService() error {
	hash, err := spec.HashService(d.service)
	d.hash = hash
	return err
}
func (d *functionDefinition) define() error {
	contract := functionContract{}
	d.value = nativeApp{
		Kind: contract.resourceKind(), Location: to.Ptr(d.config.region), Tags: d.tags(),
		Identity:   &armappcontainers.ManagedServiceIdentity{Type: to.Ptr(armappcontainers.ManagedServiceIdentityTypeUserAssigned), UserAssignedIdentities: map[string]*armappcontainers.UserAssignedIdentity{d.config.identity: {}}},
		Properties: &nativeProperties{EnvironmentID: to.Ptr(d.config.environment), Configuration: &armappcontainers.Configuration{ActiveRevisionsMode: to.Ptr(armappcontainers.ActiveRevisionsModeSingle), Ingress: &armappcontainers.Ingress{External: to.Ptr(false), TargetPort: to.Ptr(contract.targetPort()), AllowInsecure: to.Ptr(false), Transport: to.Ptr(armappcontainers.IngressTransportMethodAuto)}}, Template: &nativeTemplate{Containers: []*armappcontainers.Container{{Name: to.Ptr(d.service.Name), Image: to.Ptr(d.service.Image), Env: (hostEnvironment{config: d.config}).build(d.service), Resources: &armappcontainers.ContainerResources{CPU: to.Ptr(d.cpu()), Memory: to.Ptr(d.memory())}}}, Scale: &nativeScale{AllowScalingRuleOverride: to.Ptr(false), MinReplicas: to.Ptr(int32(0)), MaxReplicas: to.Ptr(contract.maximumReplicas())}}},
	}
	return nil
}
func (d functionDefinition) tags() map[string]*string {
	identity := resourceIdentity{}
	return map[string]*string{identity.tag(identity.managedTag()): to.Ptr(identity.manager()), identity.tag(identity.providerTag()): to.Ptr((functionContract{}).providerName()), identity.tag(identity.projectTag()): to.Ptr(d.plan.App.Project), identity.tag(identity.appTag()): to.Ptr(d.plan.App.App), identity.tag(identity.serviceTag()): to.Ptr(d.service.Name), identity.tag(identity.revisionTag()): to.Ptr(d.plan.Revision), identity.tag(identity.serviceHashTag()): to.Ptr(d.hash), identity.tag(identity.specHashTag()): to.Ptr(d.plan.SpecHash)}
}
func (functionDefinition) cpu() float64   { return 0.5 }
func (functionDefinition) memory() string { return "1Gi" }
func (d functionDefinition) result(err error) (nativeApp, error) {
	if err != nil {
		return nativeApp{}, err
	}
	return d.value, nil
}
