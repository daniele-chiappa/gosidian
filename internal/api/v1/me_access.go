package v1

import (
	"net/http"

	"github.com/gosidian/gosidian/internal/authz"
	"github.com/gosidian/gosidian/internal/webauth"
)

// accessProjectView is one row of an access view: what an account may do on
// a project and why.
type accessProjectView struct {
	Name       string `json:"name"`
	Visibility string `json:"visibility"`
	// Level is the effective access: read | write | admin (never "none": rows
	// the account cannot read are not listed, so existence is not revealed).
	Level string `json:"level"`
	// Via lists the reasons that produced the level: "owner", "public",
	// "internal", "grant:<level>".
	Via []string `json:"via"`
}

// accessView is the payload of GET /me/access and GET /admin/users/{id}/access.
type accessView struct {
	UserID string `json:"user_id"`
	Role   string `json:"role"`
	// Restricted accounts ignore project visibility and see only their
	// grants; CanCreateProjects is the capability of creating projects;
	// PersonalProject is the account's own project when it exists.
	Restricted        bool                `json:"restricted"`
	CanCreateProjects bool                `json:"can_create_projects"`
	PersonalProject   string              `json:"personal_project,omitempty"`
	Projects          []accessProjectView `json:"projects"`
}

// handleMeAccess returns the caller's effective access to every project they
// may read, so the SPA can gate per-project controls and draw the visibility
// cues from one request.
func (r *Router) handleMeAccess(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
		return
	}
	ru := UserFromContext(req.Context())
	if ru == nil {
		WriteError(w, http.StatusUnauthorized, CodeAuthTokenInvalid, "no user in context")
		return
	}
	u, ok := r.deps.Auth.WebAuth.UserByID(ru.ID)
	if !ok {
		// The synthetic open-mode guest has no account record: a guest
		// principal with no grants, no capabilities.
		r.writeAccessView(w, webauth.User{ID: ru.ID, Username: ru.Username, Role: ru.Role})
		return
	}
	r.writeAccessView(w, *u)
}

// writeAccessView computes and writes the access view for an account.
// Shared by the self-service endpoint and the owner's per-user preview.
func (r *Router) writeAccessView(w http.ResponseWriter, u webauth.User) {
	p := authz.Principal{UserID: u.ID, Role: u.Role, Restricted: u.Restricted}
	projs, err := r.deps.Vault.Projects()
	if err != nil {
		WriteError(w, http.StatusInternalServerError, CodeServerInternal, err.Error())
		return
	}
	cfg := r.accessConfig()
	out := make([]accessProjectView, 0, len(projs))
	for _, proj := range projs {
		lvl, via := p.Explain(proj.Name, cfg)
		if lvl < authz.LevelRead {
			continue
		}
		out = append(out, accessProjectView{
			Name:       proj.Name,
			Visibility: r.projectVisibility(proj.Name),
			Level:      lvl.String(),
			Via:        via,
		})
	}
	WriteJSON(w, http.StatusOK, accessView{
		UserID:            u.ID,
		Role:              string(u.Role),
		Restricted:        u.Restricted,
		CanCreateProjects: u.CanCreateProjects(),
		PersonalProject:   r.personalProjectOf(u),
		Projects:          out,
	})
}
