package oauth

import (
	"encoding/json"
	"net/http"
)

// asMetadata is the RFC 8414 document. token_endpoint_auth_methods_supported
// "none" together with client_id_metadata_document_supported is what makes
// claude.ai and ChatGPT pick the metadata-document path over DCR.
type asMetadata struct {
	Issuer                                 string   `json:"issuer"`
	AuthorizationEndpoint                  string   `json:"authorization_endpoint"`
	TokenEndpoint                          string   `json:"token_endpoint"`
	RegistrationEndpoint                   string   `json:"registration_endpoint"`
	RevocationEndpoint                     string   `json:"revocation_endpoint"`
	ResponseTypesSupported                 []string `json:"response_types_supported"`
	ResponseModesSupported                 []string `json:"response_modes_supported"`
	GrantTypesSupported                    []string `json:"grant_types_supported"`
	CodeChallengeMethodsSupported          []string `json:"code_challenge_methods_supported"`
	TokenEndpointAuthMethodsSupported      []string `json:"token_endpoint_auth_methods_supported"`
	RevocationEndpointAuthMethodsSupported []string `json:"revocation_endpoint_auth_methods_supported"`
	ScopesSupported                        []string `json:"scopes_supported"`
	ClientIDMetadataDocumentSupported      bool     `json:"client_id_metadata_document_supported"`
	ServiceDocumentation                   string   `json:"service_documentation,omitempty"`
}

// prMetadata is the RFC 9728 document for the MCP resource.
type prMetadata struct {
	Resource               string   `json:"resource"`
	AuthorizationServers   []string `json:"authorization_servers"`
	ScopesSupported        []string `json:"scopes_supported"`
	BearerMethodsSupported []string `json:"bearer_methods_supported"`
	ResourceName           string   `json:"resource_name"`
	ResourceDocumentation  string   `json:"resource_documentation,omitempty"`
}

const docsURL = "https://github.com/daniele-chiappa/gosidian/blob/main/docs/mcp/authentication.md"

func (s *Server) handleASMetadata(w http.ResponseWriter, r *http.Request) {
	if !metadataMethod(w, r) {
		return
	}
	iss := s.cfg.Issuer
	writeMetadata(w, asMetadata{
		Issuer:                                 iss,
		AuthorizationEndpoint:                  iss + PathAuthorize,
		TokenEndpoint:                          iss + PathToken,
		RegistrationEndpoint:                   iss + PathRegister,
		RevocationEndpoint:                     iss + PathRevoke,
		ResponseTypesSupported:                 []string{"code"},
		ResponseModesSupported:                 []string{"query"},
		GrantTypesSupported:                    []string{"authorization_code", "refresh_token"},
		CodeChallengeMethodsSupported:          []string{"S256"},
		TokenEndpointAuthMethodsSupported:      []string{"none"},
		RevocationEndpointAuthMethodsSupported: []string{"none"},
		ScopesSupported:                        []string{ScopeRead, ScopeWrite, ScopeOffline},
		ClientIDMetadataDocumentSupported:      true,
		ServiceDocumentation:                   docsURL,
	})
}

func (s *Server) handlePRM(w http.ResponseWriter, r *http.Request) {
	if !metadataMethod(w, r) {
		return
	}
	writeMetadata(w, prMetadata{
		Resource:               s.ResourceURL(),
		AuthorizationServers:   []string{s.cfg.Issuer},
		ScopesSupported:        []string{ScopeRead, ScopeWrite},
		BearerMethodsSupported: []string{"header"},
		ResourceName:           "gosidian",
		ResourceDocumentation:  docsURL,
	})
}

// metadataMethod allows GET/HEAD and answers CORS preflights: discovery
// documents are public and may be fetched from a browser context.
func metadataMethod(w http.ResponseWriter, r *http.Request) bool {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, Mcp-Protocol-Version")
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		return true
	case http.MethodOptions:
		w.WriteHeader(http.StatusNoContent)
		return false
	}
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	return false
}

// writeMetadata renders a discovery document: public and cacheable, unlike
// the token responses writeJSON marks no-store.
func writeMetadata(w http.ResponseWriter, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		http.Error(w, "metadata unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}
