package main

import "github.com/d31ma/heimdall/app/server/services/auth"

type serveDeviceClient struct {
	deployment string
	engine     *auth.Engine
	clients    auth.OIDCClients
	failure    error
}

func (c *serveDeviceClient) Activate() bool {
	c.clients, c.failure = loadOIDCClients(c.deployment)
	action := c.readAction()
	return action()
}

func (c *serveDeviceClient) readAction() func() bool {
	if c.failure != nil {
		return c.disabled
	}
	return c.activate
}

func (c *serveDeviceClient) activate() bool {
	if c.clients.DeviceClientID == "" {
		return false
	}
	c.engine.UseOIDCClients(c.clients)
	return true
}

func (*serveDeviceClient) disabled() bool {
	return false
}
