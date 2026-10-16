package main

import "github.com/d31ma/heimdall/app/server/services/enroll"

type serveApplication struct {
	authority     *serveAuthority
	material      *enroll.Material
	deviceEnabled bool
	failure       error
}

func (a *serveApplication) Run() error {
	device := serveDeviceClient{deployment: a.authority.storage.settings.deployment, engine: a.authority.engine}
	a.deviceEnabled = device.Activate()
	a.material, a.failure = enroll.Load(a.authority.storage.settings.deployment)
	action := a.materialAction()
	return action()
}

func (a *serveApplication) materialAction() func() error {
	if a.failure != nil {
		return a.failed
	}
	return a.startBackground
}

func (a *serveApplication) startBackground() error {
	background := serveBackground{application: a}
	return background.Run()
}

func (a *serveApplication) failed() error {
	return a.failure
}
