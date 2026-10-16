package registry

import (
	"github.com/d31ma/heimdall/app/server/repositories/store"
	"github.com/d31ma/heimdall/app/server/services/provider"
	"github.com/d31ma/heimdall/app/server/utils/sequence"
)

type registryCollection[Document any] struct {
	collection store.Collection[Document]
	result     *Result
	prefix     string
	prune      bool
	documents  map[string]Document
	declared   map[string]bool
	loaded     []Document
}

func (collection *registryCollection[Document]) Load(key func(Document) string) error {
	return (sequence.Steps{Actions: []func() error{collection.read, func() error { collection.index(key); return nil }}}).Run()
}

func (collection *registryCollection[Document]) Key(project string, name string) string {
	return project + "/" + name
}

func (collection *registryCollection[Document]) Mark(key string) (Document, bool) {
	collection.declared[key] = true
	document, found := collection.documents[key]
	return document, found
}

func (collection *registryCollection[Document]) Create(document Document, key string) (string, error) {
	id, err := collection.collection.Put(document)
	return id, collection.record(provider.OpCreate, key, err)
}

func (collection *registryCollection[Document]) Update(id string, changes map[string]any, key string) error {
	err := collection.collection.Patch(id, changes)
	return collection.record(provider.OpUpdate, key, err)
}

func (collection *registryCollection[Document]) Delete(id string, key string) error {
	err := collection.collection.Delete(id)
	return collection.record(provider.OpDelete, key, err)
}

func (collection *registryCollection[Document]) Prune(identifier func(Document) string, managed func(Document) bool) error {
	return (sequence.Steps{Actions: collection.retirements(identifier, managed)}).Run()
}

func (collection *registryCollection[Document]) read() error {
	documents, err := collection.collection.Find(nil)
	collection.loaded = documents
	return err
}

func (collection *registryCollection[Document]) index(key func(Document) string) {
	collection.documents = map[string]Document{}
	collection.declared = map[string]bool{}
	for _, document := range collection.loaded {
		collection.documents[key(document)] = document
	}
}

func (collection *registryCollection[Document]) record(kind provider.OperationKind, key string, err error) error {
	if err != nil {
		return err
	}
	collection.result.Changes = append(collection.result.Changes, provider.Operation{Kind: kind, Service: collection.prefix + key})
	return nil
}

func (collection *registryCollection[Document]) retirements(identifier func(Document) string, managed func(Document) bool) []func() error {
	actions := []func() error{}
	for key, document := range collection.documents {
		retirement := &registryRetirement[Document]{collection: collection, key: key, id: identifier(document), managed: managed(document)}
		actions = append(actions, retirement.Apply)
	}
	return actions
}
