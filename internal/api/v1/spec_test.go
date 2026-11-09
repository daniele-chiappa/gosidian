package v1

import (
	"os"
	"testing"

	"go.yaml.in/yaml/v3"
)

// TestOpenAPISpec_Parses keeps spec/openapi.yaml valid YAML with its paths.
func TestOpenAPISpec_Parses(t *testing.T) {
	raw, err := os.ReadFile("spec/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		OpenAPI string         `yaml:"openapi"`
		Paths   map[string]any `yaml:"paths"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("spec/openapi.yaml: %v", err)
	}
	for _, p := range []string{"/api/v1/version", "/api/v1/pipelines/{path}/runs", "/api/v1/runs/{id}/resume", "/api/v1/plugins", "/api/v1/plugins/apps", "/api/v1/run-files/{ticket}", "/api/v1/in/{project}", "/api/v1/search", "/api/v1/notes/{path}/history", "/api/v1/notes/{path}/history/{sha}", "/api/v1/notes/{path}/restore", "/api/v1/notes/{path}/versions", "/api/v1/notes/{path}/valid"} {
		if _, ok := doc.Paths[p]; !ok {
			t.Errorf("spec/openapi.yaml lacks %s", p)
		}
	}
}
