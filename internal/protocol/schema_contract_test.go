package protocol

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

const protocolSchemaURL = "https://mcp-browser-control.local/schema/protocol/v1.schema.json"

func TestProtocolV1SharedFixtures(t *testing.T) {
	t.Parallel()

	schemaPath := filepath.Join("..", "..", "protocol", "schema", "v1.schema.json")
	compiled := compileProtocolSchema(t, schemaPath)
	fixtureRoot := filepath.Join("..", "..", "protocol", "fixtures", "v1")

	t.Run("valid", func(t *testing.T) {
		paths := fixturePaths(t, filepath.Join(fixtureRoot, "valid"))
		for _, path := range paths {
			path := path
			t.Run(filepath.Base(path), func(t *testing.T) {
				document, payload := readJSONDocument(t, path)
				if err := compiled.Validate(document); err != nil {
					t.Fatalf("schema validation failed: %v", err)
				}
				var message Message
				if err := json.Unmarshal(payload, &message); err != nil {
					t.Fatalf("unmarshal Message: %v", err)
				}
				if err := message.Validate(); err != nil {
					t.Fatalf("Message.Validate() error = %v", err)
				}
			})
		}
	})

	t.Run("invalid", func(t *testing.T) {
		paths := fixturePaths(t, filepath.Join(fixtureRoot, "invalid"))
		for _, path := range paths {
			path := path
			t.Run(filepath.Base(path), func(t *testing.T) {
				document, _ := readJSONDocument(t, path)
				if err := compiled.Validate(document); err == nil {
					t.Fatal("schema validation error = nil")
				}
			})
		}
	})
}

func compileProtocolSchema(t *testing.T, path string) *jsonschema.Schema {
	t.Helper()
	document, _ := readJSONDocument(t, path)
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(protocolSchemaURL, document); err != nil {
		t.Fatalf("AddResource() error = %v", err)
	}
	compiled, err := compiler.Compile(protocolSchemaURL)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	return compiled
}

func fixturePaths(t *testing.T, directory string) []string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(directory, "*.json"))
	if err != nil {
		t.Fatalf("Glob() error = %v", err)
	}
	if len(paths) == 0 {
		t.Fatalf("no JSON fixtures found in %s", directory)
	}
	return paths
}

func readJSONDocument(t *testing.T, path string) (any, []byte) {
	t.Helper()
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", path, err)
	}
	var document any
	if err := json.Unmarshal(payload, &document); err != nil {
		t.Fatalf("Unmarshal(%s) error = %v", path, err)
	}
	return document, payload
}
