package artifacts

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const artifactURITemplate = "browser://artifacts/{artifactId}"

// RegisterResources exposes artifact data and metadata through MCP resources.
func (s *Store) RegisterResources(mcpServer *server.MCPServer) {
	mcpServer.AddResourceTemplate(
		mcp.NewResourceTemplate(
			artifactURITemplate,
			"Browser artifact",
			mcp.WithTemplateDescription("Temporary screenshot, PDF, trace, HAR, or large page result"),
		),
		s.readResource,
	)
	mcpServer.AddResourceTemplate(
		mcp.NewResourceTemplate(
			artifactURITemplate+"/metadata",
			"Browser artifact metadata",
			mcp.WithTemplateDescription("MIME type, size, expiration, and redaction metadata for a browser artifact"),
			mcp.WithTemplateMIMEType("application/json"),
		),
		s.readMetadataResource,
	)
}

func (s *Store) readResource(
	ctx context.Context,
	request mcp.ReadResourceRequest,
) ([]mcp.ResourceContents, error) {
	id, err := artifactIDFromURI(request.Params.URI, false)
	if err != nil {
		return nil, err
	}
	metadata, data, err := s.Read(ctx, id)
	if err != nil {
		return nil, err
	}
	if textMIMEType(metadata.MIMEType) && utf8.Valid(data) {
		return []mcp.ResourceContents{mcp.TextResourceContents{
			URI:      metadata.URI,
			MIMEType: metadata.MIMEType,
			Text:     string(data),
		}}, nil
	}
	return []mcp.ResourceContents{mcp.BlobResourceContents{
		URI:      metadata.URI,
		MIMEType: metadata.MIMEType,
		Blob:     base64.StdEncoding.EncodeToString(data),
	}}, nil
}

func (s *Store) readMetadataResource(
	ctx context.Context,
	request mcp.ReadResourceRequest,
) ([]mcp.ResourceContents, error) {
	id, err := artifactIDFromURI(request.Params.URI, true)
	if err != nil {
		return nil, err
	}
	metadata, _, err := s.Read(ctx, id)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(metadata)
	if err != nil {
		return nil, fmt.Errorf("marshal artifact resource metadata: %w", err)
	}
	return []mcp.ResourceContents{mcp.TextResourceContents{
		URI:      metadata.URI + "/metadata",
		MIMEType: "application/json",
		Text:     string(payload),
	}}, nil
}

func artifactIDFromURI(uri string, metadata bool) (string, error) {
	parsed, err := url.Parse(uri)
	if err != nil || parsed.Scheme != "browser" || parsed.Host != "artifacts" ||
		parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("invalid artifact resource URI %q", uri)
	}
	segments := strings.Split(strings.Trim(parsed.EscapedPath(), "/"), "/")
	wantSegments := 1
	if metadata {
		wantSegments = 2
	}
	if len(segments) != wantSegments || (metadata && segments[1] != "metadata") {
		return "", fmt.Errorf("invalid artifact resource URI %q", uri)
	}
	id, err := url.PathUnescape(segments[0])
	if err != nil {
		return "", ErrInvalidID
	}
	if err := validateID(id); err != nil {
		return "", err
	}
	return id, nil
}

func textMIMEType(mimeType string) bool {
	mediaType := strings.ToLower(strings.TrimSpace(strings.SplitN(mimeType, ";", 2)[0]))
	return strings.HasPrefix(mediaType, "text/") ||
		mediaType == "application/json" ||
		mediaType == "application/javascript" ||
		mediaType == "application/xml" ||
		strings.HasSuffix(mediaType, "+json") ||
		strings.HasSuffix(mediaType, "+xml")
}
