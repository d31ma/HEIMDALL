package registry

import "github.com/d31ma/heimdall/app/server/utils/sequence"

type registryDeclarations[Declaration any, Document any] struct {
	collection   *registryCollection[Document]
	declarations []Declaration
	key          func(Document) string
	write        func(Declaration) error
}

func (declarations *registryDeclarations[Declaration, Document]) Apply() error {
	return (sequence.Steps{Actions: []func() error{declarations.read, declarations.apply}}).Run()
}

func (declarations *registryDeclarations[Declaration, Document]) read() error {
	return declarations.collection.Load(declarations.key)
}

func (declarations *registryDeclarations[Declaration, Document]) apply() error {
	return (sequence.Steps{Actions: declarations.actions()}).Run()
}

func (declarations *registryDeclarations[Declaration, Document]) actions() []func() error {
	actions := []func() error{}
	for _, declaration := range declarations.declarations {
		actions = append(actions, func() error { return declarations.write(declaration) })
	}
	return actions
}
