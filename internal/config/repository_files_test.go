package config

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestRepositoryYAMLParses(t *testing.T) {
	paths := []string{
		filepath.Join("..", "..", ".github", "workflows", "ci.yml"),
		filepath.Join("..", "..", "configs", "server.example.yaml"),
		filepath.Join("..", "..", "configs", "client.example.yaml"),
		filepath.Join("..", "..", "testdata", "server.valid.yaml"),
		filepath.Join("..", "..", "testdata", "client.valid.yaml"),
	}
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			raw, err := os.ReadFile(path)
			if err != nil { t.Fatal(err) }
			var document any
			if err := yaml.Unmarshal(raw, &document); err != nil { t.Fatalf("invalid YAML: %v", err) }
			if document == nil { t.Fatal("YAML document is empty") }
		})
	}
}
