package azurefunctions

import (
	"context"
	"time"

	"github.com/d31ma/heimdall/internal/provider"
)

type observer struct {
	provider *Provider
	client   *armClient
}

func (o observer) observe(ctx context.Context, target provider.Target, app provider.AppRef) (provider.LiveState, error) {
	config, err := (targetConfig{}).parse(target)
	if err != nil {
		return provider.LiveState{}, err
	}
	client, err := (clientFactory{provider: o.provider}).create(config)
	if err != nil {
		return provider.LiveState{}, err
	}
	return (observer{client: client}).read(ctx, target.ID, app)
}

func (o observer) read(ctx context.Context, target string, app provider.AppRef) (provider.LiveState, error) {
	c := o.client
	apps, err := c.list(ctx)
	if err != nil {
		return provider.LiveState{}, err
	}
	state := provider.LiveState{App: app, Target: target, ReadAt: time.Now().UTC(), Services: map[string]provider.ServiceState{}}
	first := true
	for _, deployed := range apps {
		if !(resourceIdentity{}).owned(deployed, app) {
			continue
		}
		service := deployed.tagValue("service")
		if _, duplicate := state.Services[service]; duplicate || (optionalValue[string]{}).read(deployed.Name) != (resourceIdentity{}).name(app, service) {
			return provider.LiveState{}, (&AdapterError{Code: "HD0474", Message: "duplicate or mismatched managed function resource identity"})
		}
		image := ""
		readback := revisionReadback{client: c}
		readyRevision, err := readback.inspect(ctx, deployed)
		if err != nil {
			return provider.LiveState{}, err
		}
		message := "native deployment health; ready revision image is unobserved; execution inventory is not observed by this adapter"
		if readyRevision != nil && readyRevision.Properties != nil && readyRevision.Properties.Template != nil && len(readyRevision.Properties.Template.Containers) == 1 && readyRevision.Properties.Template.Containers[0] != nil {
			image = (optionalValue[string]{}).read(readyRevision.Properties.Template.Containers[0].Image)
			message = "native deployment health and immutable ready revision image; execution inventory is not observed by this adapter"
		}
		health := readback.compare(deployed, *deployed, readyRevision)
		state.Services[service] = provider.ServiceState{Health: health, Image: image, Message: message}
		revision, hash := deployed.tagValue("revision"), deployed.tagValue("spec-hash")
		if first {
			state.Revision, state.SpecHash, first = revision, hash, false
		} else {
			if state.Revision != revision {
				state.Revision = ""
			}
			if state.SpecHash != hash {
				state.SpecHash = ""
			}
		}
	}
	return state, nil
}

func (o observer) instances(ctx context.Context, target provider.Target, app provider.AppRef) ([]provider.Instance, error) {
	if _, err := o.observe(ctx, target, app); err != nil {
		return nil, err
	}
	return []provider.Instance{}, nil
}

func (o observer) events(ctx context.Context, target provider.Target, app provider.AppRef) ([]provider.Event, error) {
	state, err := o.observe(ctx, target, app)
	if err != nil {
		return nil, err
	}
	return []provider.Event{{At: state.ReadAt, Type: "deployment-observation", Message: "native deployment health " + string(state.Rollup()), Source: "azurefunctions"}}, nil
}
