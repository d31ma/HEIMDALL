package api

import (
	"encoding/json"
	"net/http"
	"sort"

	"github.com/d31ma/heimdall/internal/store"
)

// The registry routes are ADR 0010's whole interactive surface: bind the
// root repository, unbind it, read the binding, ask for a sync. Everything
// the registry contains is a commit to that repository.

func (s *Server) registryUnavailable(w http.ResponseWriter) bool {
	if s.Registry == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"code": "HD0503", "message": "this control plane runs no registry engine",
		})
		return true
	}
	return false
}

func (s *Server) registryBind(w http.ResponseWriter, r *http.Request) {
	if s.registryUnavailable(w) {
		return
	}
	var request struct {
		URL              string `json:"url"`
		Ref              string `json:"ref"`
		Path             string `json:"path"`
		CredentialRef    string `json:"credential_ref"`
		RequireSignature bool   `json:"require_signature"`
		Prune            bool   `json:"prune"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBytes)).Decode(&request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"code": "HD0400", "message": "the request body is not valid JSON",
		})
		return
	}
	binding, err := s.Registry.Bind(store.RootBinding{
		URL: request.URL, Ref: request.Ref, Path: request.Path,
		CredentialRef: request.CredentialRef, RequireSignature: request.RequireSignature,
		Prune: request.Prune, BoundBy: Principal(r),
	})
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, binding)
}

func (s *Server) registryUnbind(w http.ResponseWriter, r *http.Request) {
	if s.registryUnavailable(w) {
		return
	}
	if err := s.Registry.Unbind(); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "unbound"})
}

func (s *Server) registryStatus(w http.ResponseWriter, r *http.Request) {
	if s.registryUnavailable(w) {
		return
	}
	binding, found, err := s.Registry.Binding()
	if err != nil {
		s.fail(w, err)
		return
	}
	if !found {
		writeJSON(w, http.StatusOK, map[string]any{"bound": false})
		return
	}
	// The recent registry syncs ride along: the page's question is "is the
	// registry converged, and what changed last" — one answer, one route.
	syncs, err := store.In[store.Operation](s.Store, store.Operations).
		Find(store.Equals("reason_code", "registry_sync"))
	if err != nil {
		s.fail(w, err)
		return
	}
	sort.Slice(syncs, func(a, b int) bool { return syncs[a].StartedAt.After(syncs[b].StartedAt) })
	if len(syncs) > 20 {
		syncs = syncs[:20]
	}
	writeJSON(w, http.StatusOK, map[string]any{"bound": true, "binding": binding, "syncs": syncs})
}

func (s *Server) registrySync(w http.ResponseWriter, r *http.Request) {
	if s.registryUnavailable(w) {
		return
	}
	result, err := s.Registry.Sync(r.Context(), Principal(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
