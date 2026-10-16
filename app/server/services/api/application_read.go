package api

import "net/http"

type applicationRead struct {
	server  *Server
	writer  http.ResponseWriter
	request *http.Request
	state   applicationReadState
}

func (a *applicationRead) Read() {
	application, found := a.server.appID(a.writer, a.request)
	a.state.response.Application = application
	action := a.applicationAction(found)
	action()
}

func (a *applicationRead) applicationAction(found bool) func() {
	if !found {
		return a.finished
	}
	return a.readStatus
}

func (a *applicationRead) readStatus() {
	a.state.response.Status, a.state.failure = a.server.Reconcile.Status(a.request.Context(), a.state.response.Application.ID)
	action := a.statusAction()
	action()
}

func (a *applicationRead) statusAction() func() {
	if a.state.failure != nil {
		return a.failed
	}
	return a.readRevisions
}

func (a *applicationRead) readRevisions() {
	a.state.revisions, a.state.failure = a.server.Reconcile.Revisions(a.state.response.Application.ID, 1)
	action := a.revisionsAction()
	action()
}

func (a *applicationRead) revisionsAction() func() {
	if a.state.failure != nil {
		return a.write
	}
	return a.readTopology
}

func (a *applicationRead) readTopology() {
	action := a.topologyAction()
	action()
}

func (a *applicationRead) topologyAction() func() {
	if len(a.state.revisions) == 0 {
		return a.write
	}
	return a.buildTopology
}

func (a *applicationRead) buildTopology() {
	for _, service := range a.state.revisions[0].Spec.Services {
		a.state.response.Topology = append(a.state.response.Topology, topologyService{
			Name: service.Name, Image: service.Image, DependsOn: service.DependsOn,
			Wave: service.Wave, Replicas: service.Replicas, Function: service.Function,
		})
	}
	a.write()
}

func (a *applicationRead) write() {
	writeJSON(a.writer, http.StatusOK, a.state.response)
}

func (a *applicationRead) failed() {
	a.server.fail(a.writer, a.state.failure)
}

func (*applicationRead) finished() {}
