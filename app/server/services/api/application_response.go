package api

import (
	"github.com/d31ma/heimdall/app/server/repositories/store"
	"github.com/d31ma/heimdall/app/server/utils/diff"
)

type applicationResponse struct {
	Application store.Application `json:"application"`
	Status      diff.Summary      `json:"status"`
	Topology    []topologyService `json:"topology"`
}
