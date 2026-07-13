package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v5"
	"gopkg.in/yaml.v3"
)

func TestExamplesMatchPublishedSchema(t *testing.T) {
	root := filepath.Join("..", "..")
	compiler := jsonschema.NewCompiler()
	compiler.Draft = jsonschema.Draft2020
	schema, err := compiler.Compile(filepath.Join(root, "schemas", "config.v1.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(root, "examples", "*.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 3 {
		t.Fatalf("expected example configurations, got %v", files)
	}
	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			raw, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			var value any
			if err = yaml.Unmarshal(raw, &value); err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			var document any
			if err = json.Unmarshal(encoded, &document); err != nil {
				t.Fatal(err)
			}
			if err = schema.Validate(document); err != nil {
				t.Fatal(err)
			}
		})
	}
}
