package tools

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/hightemp/go_mcp_browser_ext_tool/internal/protocol"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func TestServiceRegistersBrowserResources(t *testing.T) {
	t.Parallel()

	service, _, _ := newTestService(t)
	mcpServer := server.NewMCPServer("test", "1.0.0")
	service.Register(mcpServer)

	resourcesResponse := mcpServer.HandleMessage(context.Background(), json.RawMessage(`{
		"jsonrpc":"2.0","id":1,"method":"resources/list"
	}`))
	resources, ok := resourcesResponse.(mcp.JSONRPCResponse)
	if !ok {
		t.Fatalf("resources/list response type = %T", resourcesResponse)
	}
	resourceResult, ok := resources.Result.(mcp.ListResourcesResult)
	if !ok || len(resourceResult.Resources) != 1 {
		t.Fatalf("resources/list result = %#v", resources.Result)
	}
	if resourceResult.Resources[0].URI != browserInstancesURI ||
		resourceResult.Resources[0].MIMEType != resourceMIMEType {
		t.Fatalf("resource = %#v", resourceResult.Resources[0])
	}

	templatesResponse := mcpServer.HandleMessage(context.Background(), json.RawMessage(`{
		"jsonrpc":"2.0","id":2,"method":"resources/templates/list"
	}`))
	templates, ok := templatesResponse.(mcp.JSONRPCResponse)
	if !ok {
		t.Fatalf("resources/templates/list response type = %T", templatesResponse)
	}
	templateResult, ok := templates.Result.(mcp.ListResourceTemplatesResult)
	if !ok || len(templateResult.ResourceTemplates) != 3 {
		t.Fatalf("resources/templates/list result = %#v", templates.Result)
	}
	wantTemplates := map[string]bool{
		"browser://instances/{browserId}":              false,
		"browser://instances/{browserId}/tabs":         false,
		"browser://instances/{browserId}/capabilities": false,
	}
	for _, template := range templateResult.ResourceTemplates {
		if _, expected := wantTemplates[template.URITemplate.Raw()]; !expected {
			t.Errorf("unexpected template %q", template.URITemplate.Raw())
		} else {
			wantTemplates[template.URITemplate.Raw()] = true
		}
	}
	for template, found := range wantTemplates {
		if !found {
			t.Errorf("template %q was not registered", template)
		}
	}

	readResponse := mcpServer.HandleMessage(context.Background(), json.RawMessage(`{
		"jsonrpc":"2.0","id":3,"method":"resources/read",
		"params":{"uri":"browser://instances/browser-a/capabilities"}
	}`))
	read, ok := readResponse.(mcp.JSONRPCResponse)
	if !ok {
		t.Fatalf("resources/read response type = %T", readResponse)
	}
	readResult, ok := read.Result.(mcp.ReadResourceResult)
	if !ok {
		t.Fatalf("resources/read result = %#v", read.Result)
	}
	readJSON := decodeResourceJSON(t, readResult.Contents)
	if readJSON["browserId"] != "browser-a" {
		t.Fatalf("resources/read browserId = %#v", readJSON["browserId"])
	}
}

func TestBrowserMetadataResources(t *testing.T) {
	t.Parallel()

	service, _, connectionB := newTestService(t)
	if !service.registry.Disconnect("browser-b", connectionB.ID(), "browser closed") {
		t.Fatal("Disconnect() = false")
	}

	instances, err := service.browserInstancesResource(context.Background(), resourceRequest(browserInstancesURI))
	if err != nil {
		t.Fatalf("browserInstancesResource() error = %v", err)
	}
	instancesJSON := decodeResourceJSON(t, instances)
	if instancesJSON["connectedCount"] != float64(1) {
		t.Errorf("connectedCount = %#v, want 1", instancesJSON["connectedCount"])
	}
	listed, ok := instancesJSON["instances"].([]any)
	if !ok || len(listed) != 2 {
		t.Fatalf("instances = %#v, want two retained instances", instancesJSON["instances"])
	}

	instanceURI := "browser://instances/browser-a"
	instance, err := service.browserInstanceResource(context.Background(), resourceRequest(instanceURI))
	if err != nil {
		t.Fatalf("browserInstanceResource() error = %v", err)
	}
	instanceJSON := decodeResourceJSON(t, instance)
	metadata, ok := instanceJSON["instance"].(map[string]any)
	if !ok || metadata["browserId"] != "browser-a" {
		t.Fatalf("instance = %#v", instanceJSON["instance"])
	}

	capabilitiesURI := "browser://instances/browser-a/capabilities"
	capabilities, err := service.browserCapabilitiesResource(
		context.Background(),
		resourceRequest(capabilitiesURI),
	)
	if err != nil {
		t.Fatalf("browserCapabilitiesResource() error = %v", err)
	}
	capabilitiesJSON := decodeResourceJSON(t, capabilities)
	if capabilitiesJSON["browserId"] != "browser-a" {
		t.Fatalf("capabilities browserId = %#v", capabilitiesJSON["browserId"])
	}
	values, ok := capabilitiesJSON["capabilities"].([]any)
	if !ok || len(values) == 0 {
		t.Fatalf("capabilities = %#v", capabilitiesJSON["capabilities"])
	}

	_, err = service.browserInstanceResource(
		context.Background(),
		resourceRequest("browser://instances/missing"),
	)
	if protocol.ErrorFrom(err).Code != protocol.CodeBrowserNotFound {
		t.Fatalf("missing browser error = %v", err)
	}
}

func TestBrowserTabsResourceRoutesToURIInstance(t *testing.T) {
	t.Parallel()

	service, connectionA, connectionB := newTestService(t)
	contentsChannel := make(chan []mcp.ResourceContents, 1)
	errorChannel := make(chan error, 1)
	uri := "browser://instances/browser-a/tabs"
	go func() {
		contents, err := service.browserTabsResource(context.Background(), resourceRequest(uri))
		if err != nil {
			errorChannel <- err
			return
		}
		contentsChannel <- contents
	}()

	request := receiveToolMessage(t, connectionA.messages)
	if request.Command != protocol.CommandTabsList || request.BrowserID != "browser-a" {
		t.Fatalf("browser request = %#v", request)
	}
	select {
	case unexpected := <-connectionB.messages:
		t.Fatalf("browser B received unexpected request: %#v", unexpected)
	default:
	}
	wantResult := map[string]any{"tabs": []map[string]any{{"id": 7, "title": "Example"}}}
	response, err := protocol.NewResponse(request.RequestID, "browser-a", wantResult, nil)
	if err != nil {
		t.Fatalf("NewResponse() error = %v", err)
	}
	if !service.router.HandleResponse("browser-a", connectionA.ID(), response) {
		t.Fatal("HandleResponse() = false")
	}

	select {
	case err := <-errorChannel:
		t.Fatalf("browserTabsResource() error = %v", err)
	case contents := <-contentsChannel:
		resourceJSON := decodeResourceJSON(t, contents)
		if resourceJSON["browserId"] != "browser-a" {
			t.Fatalf("browserId = %#v", resourceJSON["browserId"])
		}
		result, ok := resourceJSON["result"].(map[string]any)
		if !ok || !reflect.DeepEqual(result["tabs"], []any{map[string]any{
			"id": float64(7), "title": "Example",
		}}) {
			t.Fatalf("result = %#v", resourceJSON["result"])
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for tabs resource")
	}
}

func TestResourceBrowserIDRejectsMalformedURI(t *testing.T) {
	t.Parallel()

	for _, uri := range []string{
		"https://instances/browser-a",
		"browser://instances/",
		"browser://instances/browser-a/windows",
		"browser://instances/browser-a/tabs?fresh=true",
		"browser://instances/%2F/tabs",
	} {
		if _, err := resourceBrowserID(resourceRequest(uri), "tabs"); err == nil {
			t.Errorf("resourceBrowserID(%q) error = nil", uri)
		}
	}
}

func resourceRequest(uri string) mcp.ReadResourceRequest {
	return mcp.ReadResourceRequest{Params: mcp.ReadResourceParams{URI: uri}}
}

func decodeResourceJSON(t *testing.T, contents []mcp.ResourceContents) map[string]any {
	t.Helper()
	if len(contents) != 1 {
		t.Fatalf("resource content count = %d, want 1", len(contents))
	}
	text, ok := mcp.AsTextResourceContents(contents[0])
	if !ok {
		t.Fatalf("resource content type = %T", contents[0])
	}
	if text.MIMEType != resourceMIMEType {
		t.Errorf("MIME type = %q", text.MIMEType)
	}
	var value map[string]any
	if err := json.Unmarshal([]byte(text.Text), &value); err != nil {
		t.Fatalf("decode resource JSON: %v", err)
	}
	if _, err := time.Parse(time.RFC3339Nano, value["timestamp"].(string)); err != nil {
		t.Errorf("timestamp = %#v: %v", value["timestamp"], err)
	}
	return value
}
