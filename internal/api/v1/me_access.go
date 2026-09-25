package v1

import (
	"net/http"

	"github.com/gosidian/gosidian/internal/authz"
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
	UserID   string              `json:"user_id"`
	Role     string              `json:"role"`
	Projects []accessProjectView `json:"projects"`
}

// handleMeAccess returns the caller's effective access to every project they
// may read, so the SPA can gate per-project controls and draw the visibility
// cues from one request.
func (r *Router) handleMeAccess(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
		return
	}
	r.writeAccessView(w, principalFromContext(req))
}

// writeAccessView computes and writes the access view for a principal.
// Shared by the self-service endpoint and the owner's per-user preview.
func (r *Router) writeAccessView(w http.ResponseWriter, p authz.Principal) {
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
	WriteJSON(w, http.StatusOK, accessView{UserID: p.UserID, Role: string(p.Role), Projects: out})
}
