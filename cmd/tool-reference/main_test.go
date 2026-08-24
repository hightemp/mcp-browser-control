package main

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

func TestCapabilityCatalogMatchesRegisteredTools(t *testing.T) {
	t.Parallel()

	registered, err := registeredTools()
	if err != nil {
		t.Fatalf("registeredTools() error = %v", err)
	}
	if err := validateCatalog(registered); err != nil {
		t.Fatalf("validateCatalog() error = %v", err)
	}
	if len(registered) != len(toolCapabilities) {
		t.Fatalf("registered tools = %d, capability entries = %d", len(registered), len(toolCapabilities))
	}
}

func TestGeneratedReferenceDocumentsEveryTool(t *testing.T) {
	t.Parallel()

	document, err := renderReference()
	if err != nil {
		t.Fatalf("renderReference() error = %v", err)
	}
	registered, err := registeredTools()
	if err != nil {
		t.Fatalf("registeredTools() error = %v", err)
	}
	text := string(document)
	for _, tool := range registered {
		heading := "### `" + tool.Name + "`"
		if strings.Count(text, heading) != 1 {
			t.Errorf("generated reference heading %q count is not one", heading)
		}
	}
	for _, section := range []string{
		"MCP profile:",
		"Extension capability:",
		"Permissions:",
		"Result:",
		"Errors:",
		"Input schema:",
		"Example MCP tool payload:",
	} {
		if count := strings.Count(text, section); count != len(registered) {
			t.Errorf("section %q count = %d, want %d", section, count, len(registered))
		}
	}
}

func TestCommittedReferenceIsCurrent(t *testing.T) {
	t.Parallel()

	want, err := renderReference()
	if err != nil {
		t.Fatalf("renderReference() error = %v", err)
	}
	got, err := os.ReadFile("../../docs/tool-reference.md")
	if err != nil {
		t.Fatalf("read committed tool reference: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("docs/tool-reference.md is stale; run make tool-reference")
	}
}

func TestExampleOverridesReferToRegisteredTools(t *testing.T) {
	t.Parallel()

	registered, err := registeredTools()
	if err != nil {
		t.Fatalf("registeredTools() error = %v", err)
	}
	known := make(map[string]bool, len(registered))
	for _, tool := range registered {
		known[tool.Name] = true
	}
	for name := range exampleOverrides {
		if !known[name] {
			t.Errorf("example override refers to unregistered tool %q", name)
		}
	}
}

func TestGeneratedExamplesMatchRegisteredSchemas(t *testing.T) {
	t.Parallel()

	registered, err := registeredTools()
	if err != nil {
		t.Fatalf("registeredTools() error = %v", err)
	}
	for _, tool := range registered {
		tool := tool
		t.Run(tool.Name, func(t *testing.T) {
			t.Parallel()
			schemaJSON, err := json.Marshal(tool.InputSchema)
			if err != nil {
				t.Fatalf("marshal schema: %v", err)
			}
			var schemaDocument any
			if err := json.Unmarshal(schemaJSON, &schemaDocument); err != nil {
				t.Fatalf("decode schema: %v", err)
			}
			compiler := jsonschema.NewCompiler()
			resource := "https://example.invalid/tool-schema/" + tool.Name
			if err := compiler.AddResource(resource, schemaDocument); err != nil {
				t.Fatalf("AddResource() error = %v", err)
			}
			compiled, err := compiler.Compile(resource)
			if err != nil {
				t.Fatalf("Compile() error = %v", err)
			}
			example, err := exampleFor(tool)
			if err != nil {
				t.Fatalf("exampleFor() error = %v", err)
			}
			exampleJSON, err := json.Marshal(example)
			if err != nil {
				t.Fatalf("marshal example: %v", err)
			}
			var exampleDocument any
			if err := json.Unmarshal(exampleJSON, &exampleDocument); err != nil {
				t.Fatalf("decode example: %v", err)
			}
			if err := compiled.Validate(exampleDocument); err != nil {
				t.Fatalf("generated example does not match schema: %v", err)
			}
		})
	}
}
