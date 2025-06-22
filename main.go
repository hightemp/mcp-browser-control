package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func main() {
	var transport string
	var host string
	var port string
	flag.StringVar(&transport, "t", "sse", "Transport type (stdio or sse)")
	flag.StringVar(&host, "h", "0.0.0.0", "Host of sse server")
	flag.StringVar(&port, "p", "8894", "Port of sse server")
	flag.Parse()

	mcpServer := server.NewMCPServer(
		"go_mcp_browser_ext_tool",
		"1.0.0",
	)

	appendToFileTool := mcp.NewTool("github_append_to_file",
		mcp.WithDescription("Append text to the end of a file"),
		mcp.WithString("path",
			mcp.Required(),
			mcp.Description("Path to the file in the repository"),
		),
		mcp.WithString("content",
			mcp.Required(),
			mcp.Description("Content to append"),
		),
		mcp.WithString("message",
			mcp.Required(),
			mcp.Description("Commit message"),
		),
		mcp.WithString("branch",
			mcp.Description("Branch name (optional, defaults to the default branch)"),
		),
	)
	mcpServer.AddTool(appendToFileTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return githubAppendToFileHandler(ctx, request, githubClient)
	})

	if transport == "sse" {
		sseServer := server.NewSSEServer(mcpServer, server.WithBaseURL(fmt.Sprintf("http://localhost:%s", port)))
		log.Printf("SSE server listening on %s:%s URL: http://127.0.0.1:%s/sse", host, port, port)
		if err := sseServer.Start(fmt.Sprintf("%s:%s", host, port)); err != nil {
			log.Fatalf("Server error: %v", err)
		}
	} else {
		if err := server.ServeStdio(mcpServer); err != nil {
			log.Fatalf("Server error: %v", err)
		}
	}
}

func githubAppendToFileHandler(ctx context.Context, request mcp.CallToolRequest, client *GitHubClient) (*mcp.CallToolResult, error) {
	args, ok := request.Params.Arguments.(map[string]interface{})
	if !ok {
		return nil, errors.New("arguments must be a map")
	}

	return mcp.NewToolResultText(string(jsonResult)), nil
}
