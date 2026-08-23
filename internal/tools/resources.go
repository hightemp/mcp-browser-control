package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/hightemp/go_mcp_browser_ext_tool/internal/protocol"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	browserInstancesURI = "browser://instances"
	resourceMIMEType    = "application/json"
)

func (s *Service) registerResources(mcpServer *server.MCPServer) {
	mcpServer.AddResource(
		mcp.NewResource(
			browserInstancesURI,
			"Browser instances",
			mcp.WithResourceDescription("Connected and retained browser extension instances"),
			mcp.WithMIMEType(resourceMIMEType),
		),
		s.browserInstancesResource,
	)

	for _, registration := range []struct {
		template    string
		name        string
		description string
		handler     server.ResourceTemplateHandlerFunc
	}{
		{
			template:    "browser://instances/{browserId}",
			name:        "Browser instance",
			description: "Metadata and connection state for one browser instance",
			handler:     s.browserInstanceResource,
		},
		{
			template:    "browser://instances/{browserId}/tabs",
			name:        "Browser tabs",
			description: "Live tabs reported by one connected browser instance",
			handler:     s.browserTabsResource,
		},
		{
			template:    "browser://instances/{browserId}/capabilities",
			name:        "Browser capabilities",
			description: "Runtime capabilities and permissions for one browser instance",
			handler:     s.browserCapabilitiesResource,
		},
	} {
		mcpServer.AddResourceTemplate(
			mcp.NewResourceTemplate(
				registration.template,
				registration.name,
				mcp.WithTemplateDescription(registration.description),
				mcp.WithTemplateMIMEType(resourceMIMEType),
			),
			registration.handler,
		)
	}
}

func (s *Service) browserInstancesResource(
	_ context.Context,
	request mcp.ReadResourceRequest,
) ([]mcp.ResourceContents, error) {
	if request.Params.URI != browserInstancesURI {
		return nil, fmt.Errorf("unsupported browser instances resource URI %q", request.Params.URI)
	}
	return s.jsonResource(request.Params.URI, map[string]any{
		"instances":      s.registry.ListAll(),
		"connectedCount": s.registry.Count(),
		"timestamp":      resourceTimestamp(),
	})
}

func (s *Service) browserInstanceResource(
	_ context.Context,
	request mcp.ReadResourceRequest,
) ([]mcp.ResourceContents, error) {
	browserID, err := resourceBrowserID(request, "")
	if err != nil {
		return nil, err
	}
	browser, ok := s.registry.Get(browserID)
	if !ok {
		return nil, protocol.NewError(protocol.CodeBrowserNotFound, "browser not found", false)
	}
	return s.jsonResource(request.Params.URI, map[string]any{
		"instance":  browser,
		"timestamp": resourceTimestamp(),
	})
}

func (s *Service) browserCapabilitiesResource(
	_ context.Context,
	request mcp.ReadResourceRequest,
) ([]mcp.ResourceContents, error) {
	browserID, err := resourceBrowserID(request, "capabilities")
	if err != nil {
		return nil, err
	}
	browser, ok := s.registry.Get(browserID)
	if !ok {
		return nil, protocol.NewError(protocol.CodeBrowserNotFound, "browser not found", false)
	}
	return s.jsonResource(request.Params.URI, map[string]any{
		"browserId":    browserID,
		"browser":      browser.Browser,
		"capabilities": browser.Capabilities,
		"permissions":  browser.Permissions,
		"timestamp":    resourceTimestamp(),
	})
}

func (s *Service) browserTabsResource(
	ctx context.Context,
	request mcp.ReadResourceRequest,
) ([]mcp.ResourceContents, error) {
	browserID, err := resourceBrowserID(request, "tabs")
	if err != nil {
		return nil, err
	}
	result, err := s.router.Send(ctx, browserID, protocol.CommandTabsList, nil, map[string]any{})
	if err != nil {
		return nil, err
	}
	result, _, err = s.sanitizeBrowserResult(result)
	if err != nil {
		return nil, err
	}
	var tabs any
	if len(result) > 0 {
		if err := json.Unmarshal(result, &tabs); err != nil {
			return nil, fmt.Errorf("decode browser tabs resource: %w", err)
		}
	}
	return s.jsonResource(request.Params.URI, map[string]any{
		"browserId": browserID,
		"result":    tabs,
		"timestamp": resourceTimestamp(),
	})
}

func resourceBrowserID(request mcp.ReadResourceRequest, suffix string) (string, error) {
	parsed, err := url.Parse(request.Params.URI)
	if err != nil || parsed.Scheme != "browser" || parsed.Host != "instances" ||
		parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("invalid browser resource URI %q", request.Params.URI)
	}
	segments := strings.Split(strings.Trim(parsed.EscapedPath(), "/"), "/")
	wantSegments := 1
	if suffix != "" {
		wantSegments = 2
	}
	if len(segments) != wantSegments || segments[0] == "" ||
		(suffix != "" && segments[1] != suffix) {
		return "", fmt.Errorf("invalid browser resource URI %q", request.Params.URI)
	}
	browserID, err := url.PathUnescape(segments[0])
	if err != nil || strings.TrimSpace(browserID) == "" || strings.Contains(browserID, "/") {
		return "", fmt.Errorf("invalid browserId in resource URI %q", request.Params.URI)
	}
	return browserID, nil
}

func (s *Service) jsonResource(uri string, value any) ([]mcp.ResourceContents, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("marshal browser resource: %w", err)
	}
	payload, _, err = s.sanitizeBrowserResult(payload)
	if err != nil {
		return nil, err
	}
	return []mcp.ResourceContents{mcp.TextResourceContents{
		URI:      uri,
		MIMEType: resourceMIMEType,
		Text:     string(payload),
	}}, nil
}

func resourceTimestamp() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}
