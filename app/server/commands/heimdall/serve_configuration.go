package main

type serveConfiguration struct {
	options  serveOptions
	settings serveSettings
	failure  error
}

func (c *serveConfiguration) Load(arguments []string) (serveSettings, error) {
	c.options.Parse(arguments)
	c.settings.standby = c.options.standby
	c.settings.deployment, c.failure = deploymentDir(c.options.deployment)
	action := c.deploymentAction()
	return action()
}

func (c *serveConfiguration) deploymentAction() func() (serveSettings, error) {
	if c.failure != nil {
		return c.failed
	}
	return c.readRetention
}

func (c *serveConfiguration) readRetention() (serveSettings, error) {
	c.settings.retention, c.failure = metricsRetention()
	action := c.retentionAction()
	return action()
}

func (c *serveConfiguration) retentionAction() func() (serveSettings, error) {
	if c.failure != nil {
		return c.failed
	}
	return c.readDeployment
}

func (c *serveConfiguration) readDeployment() (serveSettings, error) {
	c.settings.loaded, c.failure = loadConfig(c.settings.deployment)
	action := c.configurationAction()
	return action()
}

func (c *serveConfiguration) configurationAction() func() (serveSettings, error) {
	if c.failure != nil {
		return c.failed
	}
	return c.readAddress
}

func (c *serveConfiguration) readAddress() (serveSettings, error) {
	c.settings.address = envOr("HD_ADDR", c.options.address)
	address := c.addressAction()
	c.settings.address = address()
	return c.settings, nil
}

func (c *serveConfiguration) addressAction() func() string {
	if c.settings.address == "" {
		return c.defaultAddress
	}
	return c.currentAddress
}

func (*serveConfiguration) defaultAddress() string {
	return "127.0.0.1:8080"
}

func (c *serveConfiguration) currentAddress() string {
	return c.settings.address
}

func (c *serveConfiguration) failed() (serveSettings, error) {
	return serveSettings{}, c.failure
}
