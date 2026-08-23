package artifacts

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func TestArtifactMCPResourcesReturnTextBlobAndMetadata(t *testing.T) {
	t.Parallel()

	store, err := New(t.TempDir(), time.Hour, WithMaxBytes(1024))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	textMetadata, err := store.Put(
		context.Background(),
		"text/html; charset=utf-8",
		[]byte("<p>safe</p>"),
		RedactionMetadata{Applied: true, Rules: []string{"form-values"}},
	)
	if err != nil {
		t.Fatalf("Put(text) error = %v", err)
	}
	binaryMetadata, err := store.Put(
		context.Background(),
		"image/png",
		[]byte{0x89, 0x50, 0x4e, 0x47},
		RedactionMetadata{},
	)
	if err != nil {
		t.Fatalf("Put(binary) error = %v", err)
	}

	mcpServer := server.NewMCPServer("test", "1.0.0")
	store.RegisterResources(mcpServer)

	text := readMCPResource(t, mcpServer, textMetadata.URI)
	textContent, ok := mcp.AsTextResourceContents(text.Contents[0])
	if !ok || textContent.Text != "<p>safe</p>" || textContent.MIMEType != "text/html; charset=utf-8" {
		t.Fatalf("text content = %#v", text.Contents[0])
	}

	binary := readMCPResource(t, mcpServer, binaryMetadata.URI)
	blobContent, ok := mcp.AsBlobResourceContents(binary.Contents[0])
	if !ok || blobContent.Blob != base64.StdEncoding.EncodeToString([]byte{0x89, 0x50, 0x4e, 0x47}) {
		t.Fatalf("blob content = %#v", binary.Contents[0])
	}

	metadataResult := readMCPResource(t, mcpServer, textMetadata.URI+"/metadata")
	metadataContent, ok := mcp.AsTextResourceContents(metadataResult.Contents[0])
	if !ok {
		t.Fatalf("metadata content = %#v", metadataResult.Contents[0])
	}
	var resourceMetadata Metadata
	if err := json.Unmarshal([]byte(metadataContent.Text), &resourceMetadata); err != nil {
		t.Fatalf("decode metadata resource: %v", err)
	}
	if resourceMetadata.ID != textMetadata.ID || !resourceMetadata.Redaction.Applied ||
		len(resourceMetadata.Redaction.Rules) != 1 {
		t.Fatalf("metadata resource = %#v", resourceMetadata)
	}
}

func TestArtifactResourceURIRejectsTraversal(t *testing.T) {
	t.Parallel()

	for _, uri := range []string{
		"browser://artifacts/../secret",
		"browser://artifacts/%2Fetc%2Fpasswd",
		"file://artifacts/" + stringsOf('A', idLength),
		"browser://artifacts/" + stringsOf('A', idLength) + "?raw=true",
	} {
		if _, err := artifactIDFromURI(uri, false); err == nil {
			t.Errorf("artifactIDFromURI(%q) error = nil", uri)
		}
	}
}

func readMCPResource(t *testing.T, mcpServer *server.MCPServer, uri string) mcp.ReadResourceResult {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "resources/read",
		"params":  map[string]string{"uri": uri},
	})
	if err != nil {
		t.Fatalf("marshal resource request: %v", err)
	}
	response := mcpServer.HandleMessage(context.Background(), payload)
	jsonRPC, ok := response.(mcp.JSONRPCResponse)
	if !ok {
		t.Fatalf("resources/read response = %#v", response)
	}
	result, ok := jsonRPC.Result.(mcp.ReadResourceResult)
	if !ok {
		t.Fatalf("resources/read result = %#v", jsonRPC.Result)
	}
	if len(result.Contents) != 1 {
		t.Fatalf("resource content count = %d", len(result.Contents))
	}
	return result
}
