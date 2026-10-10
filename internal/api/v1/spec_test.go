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
	for _, p := range []string{"/api/v1/version", "/api/v1/pipelines/{path}/runs", "/api/v1/runs/{id}/resume"} {
		if _, ok := doc.Paths[p]; !ok {
			t.Errorf("spec/openapi.yaml lacks %s", p)
		}
	}
}
