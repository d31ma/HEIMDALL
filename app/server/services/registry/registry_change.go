package registry

type registryChange struct {
	found   bool
	changed bool
	create  func() error
	update  func() error
}

func (change *registryChange) Apply() error {
	return change.action()()
}

func (change *registryChange) Update() error {
	return change.updateAction()()
}

func (change *registryChange) action() func() error {
	if !change.found {
		return change.create
	}
	return change.Update
}

func (change *registryChange) updateAction() func() error {
	if !change.changed {
		return change.unchanged
	}
	return change.update
}

func (change *registryChange) unchanged() error { return nil }
