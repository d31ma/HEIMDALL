package api

import "github.com/d31ma/heimdall/app/server/models/spec"

type topologyService struct {
	Name      string         `json:"name"`
	Image     string         `json:"image"`
	DependsOn []string       `json:"depends_on,omitempty"`
	Wave      int            `json:"wave"`
	Replicas  int            `json:"replicas"`
	Function  *spec.Function `json:"function,omitempty"`
}
