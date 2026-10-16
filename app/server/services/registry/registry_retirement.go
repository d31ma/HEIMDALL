package registry

import (
	"slices"
)

type registryRetirement[Document any] struct {
	collection *registryCollection[Document]
	key        string
	id         string
	managed    bool
	eligible   bool
}

func (retirement *registryRetirement[Document]) Apply() error {
	retirement.eligibility()
	retirement.skipped()
	return retirement.remove()
}

func (retirement *registryRetirement[Document]) eligibility() {
	retirement.eligible = !slices.Contains([]bool{retirement.collection.declared[retirement.key], !retirement.managed}, true)
}

func (retirement *registryRetirement[Document]) skipped() {
	if slices.Contains([]bool{!retirement.eligible, retirement.collection.prune}, true) {
		return
	}
	retirement.collection.result.Skipped++
}

func (retirement *registryRetirement[Document]) remove() error {
	if slices.Contains([]bool{!retirement.eligible, !retirement.collection.prune}, true) {
		return nil
	}
	return retirement.collection.Delete(retirement.id, retirement.key)
}
