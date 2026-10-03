package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/d31ma/heimdall/internal/auth"
	"github.com/d31ma/heimdall/internal/store"
)

// The SCIM 2.0 host: /scim/v2/Users and /scim/v2/Groups, the surface an
// identity provider's provisioning engine calls.
//
// Nothing here decides anything. The IdP's bearer token travels to SESAME
// with every operation and SESAME authenticates it; the SCIM JSON bodies pass
// through verbatim, because a host that "understood" them would be a second
// implementation of a spec SESAME already implements. What HEIMDALL adds is
// one thing: when a group flows past, its id and name are remembered, and any
// role mapping an administrator declared for that name is enforced as a
// SESAME grant — which is the entire mechanism behind "an Okta group grants
// operator on a project with no HEIMDALL-side user administration".

// GroupMapping declares that a directory group grants a role bundle on a
// project. It is authoritative state an administrator wrote, in hd-group-mappings.
type GroupMapping struct {
	ID      string `json:"id,omitempty"`
	Project string `json:"project"`
	// GroupName matches the SCIM group's displayName, which is the name the
	// directory administrator sees in Okta or Entra.
	GroupName string `json:"group_name"`
	// Role names one of the shipped bundles: viewer, operator, admin, owner.
	Role string `json:"role"`
	// RoleID and GrantID record what the mapping caused in SESAME once the
	// group has appeared; RoleID also lets a half-failed attempt resume.
	RoleID  string `json:"role_id,omitempty"`
	GrantID string `json:"grant_id,omitempty"`
	// GroupID is the SESAME group id once known.
	GroupID string `json:"group_id,omitempty"`
}

func scimBearer(r *http.Request) string {
	return strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
}

// scim dispatches one SCIM resource call to the matching engine operation.
func (s *Server) scim(w http.ResponseWriter, r *http.Request) {
	if s.Engine == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"code": "HD0503", "message": "authorization engine unavailable",
		})
		return
	}
	token := scimBearer(r)
	if token == "" {
		// SCIM's own error shape, so the IdP's provisioning engine logs
		// something its operator recognises.
		writeSCIMError(w, http.StatusUnauthorized, "a bearer token is required")
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBytes))
	if err != nil {
		writeSCIMError(w, http.StatusRequestEntityTooLarge, "the request body is too large")
		return
	}

	resource := r.PathValue("resource")
	id := r.PathValue("id")

	result, err := s.Engine.SCIM(r.Context(), auth.SCIMCall{
		Resource: resource, Method: r.Method, ID: id, Token: token, Body: string(body),
	})
	if err != nil {
		// SESAME's refusals include authentication failures; one uniform
		// answer avoids telling a token-guesser which part was right. (Note
		// for operators: SESAME's group-name alphabet has no spaces, so a
		// directory group named "Platform Operators" must be pushed as
		// "platform-operators" — the IdP's outbound name mapping does this.)
		writeSCIMError(w, http.StatusUnauthorized, "the request was not accepted")
		return
	}

	// The one HEIMDALL-side effect: a group that flowed past is remembered,
	// and any mapping for its name becomes a grant.
	if strings.EqualFold(resource, "Groups") && r.Method != http.MethodGet {
		s.reconcileGroupMappings(r.Context(), result)
	}

	status := http.StatusOK
	if r.Method == http.MethodPost {
		status = http.StatusCreated
	}
	w.Header().Set("Content-Type", "application/scim+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(result)
}

func writeSCIMError(w http.ResponseWriter, status int, detail string) {
	w.Header().Set("Content-Type", "application/scim+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"schemas": []string{"urn:ietf:params:scim:api:messages:2.0:Error"},
		"status":  status, "detail": detail,
	})
}

// reconcileGroupMappings makes declared mappings true for one group document
// that just flowed through: if an administrator mapped its displayName to a
// role on a project, the SESAME grant is created once and recorded.
func (s *Server) reconcileGroupMappings(ctx context.Context, groupDocument map[string]any) {
	if s.Store == nil {
		return
	}
	displayName, _ := groupDocument["displayName"].(string)
	groupID, _ := groupDocument["id"].(string)
	if displayName == "" || groupID == "" {
		return
	}

	mappings := store.In[GroupMapping](s.Store, store.GroupMappings)
	declared, err := mappings.Find(map[string]any{"group_name": displayName})
	if err != nil {
		return
	}
	for _, mapping := range declared {
		if mapping.GrantID != "" {
			continue // already enforced
		}
		roleID, grantID, err := s.Engine.GrantRoleToGroup(ctx, groupID, mapping.Role, mapping.Project, mapping.RoleID)
		if err != nil {
			// Remember a role that was created even when the grant failed, so
			// the retry on the next group event does not mint a second role.
			if roleID != "" {
				_ = mappings.Patch(mapping.ID, map[string]any{"role_id": roleID})
			}
			continue
		}
		_ = mappings.Patch(mapping.ID, map[string]any{
			"role_id": roleID, "grant_id": grantID, "group_id": groupID,
		})
	}
}

// createGroupMapping is the authorized route an administrator uses to declare
// "directory group X grants role Y on this project".
func (s *Server) createGroupMapping(w http.ResponseWriter, r *http.Request) {
	var mapping GroupMapping
	if !decodeBody(w, r, &mapping) {
		return
	}
	mapping.Project = r.PathValue("project")
	if mapping.GroupName == "" || mapping.Role == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"code": "HD0400", "message": "group_name and role are required",
		})
		return
	}
	if !auth.KnownBundle(mapping.Role) {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"code": "HD0400", "message": "role must be one of the shipped bundles: viewer, operator, admin, owner",
		})
		return
	}
	mapping.RoleID = ""
	mapping.GrantID = ""
	mapping.GroupID = ""

	id, err := store.In[GroupMapping](s.Store, store.GroupMappings).Put(mapping)
	if err != nil {
		s.fail(w, err)
		return
	}
	mapping.ID = id
	writeJSON(w, http.StatusCreated, mapping)
}

func (s *Server) listGroupMappings(w http.ResponseWriter, r *http.Request) {
	mappings, err := store.In[GroupMapping](s.Store, store.GroupMappings).
		Find(map[string]any{"project": r.PathValue("project")})
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"mappings": mappings})
}
