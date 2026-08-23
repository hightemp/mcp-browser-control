// Package app assembles and runs the MCP browser control server.
package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/hightemp/go_mcp_browser_ext_tool/internal/mcpsession"
	"github.com/hightemp/go_mcp_browser_ext_tool/internal/netguard"
	"github.com/hightemp/go_mcp_browser_ext_tool/internal/registry"
	"github.com/hightemp/go_mcp_browser_ext_tool/internal/router"
	"github.com/hightemp/go_mcp_browser_ext_tool/internal/selection"
	browsertools "github.com/hightemp/go_mcp_browser_ext_tool/internal/tools"
	websockettransport "github.com/hightemp/go_mcp_browser_ext_tool/internal/transport/websocket"
	"github.com/mark3labs/mcp-go/server"
)

const (
	serverName    = "go_mcp_browser_ext_tool"
	serverVersion = "0.2.0"
)

// Config contains process-level server configuration.
type Config struct {
	Transport      string
	MCPHost        string
	MCPPort        string
	WebSocketHost  string
	WebSocketPort  string
	CommandTimeout time.Duration
}

// Run parses args, assembles the application, and blocks until shutdown.
func Run(
	ctx context.Context,
	args []string,
	stdin io.Reader,
	stdout io.Writer,
	stderr io.Writer,
) error {
	config, err := parseConfig(args, stderr)
	if err != nil {
		return err
	}
	logger := log.New(stderr, "", log.LstdFlags)
	return run(ctx, config, stdin, stdout, logger)
}

func parseConfig(args []string, stderr io.Writer) (Config, error) {
	config := Config{}
	flags := flag.NewFlagSet(serverName, flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(
		&config.Transport,
		"t",
		"streamable-http",
		"Transport type: streamable-http, stdio, or sse",
	)
	flags.StringVar(&config.MCPHost, "h", "127.0.0.1", "MCP HTTP host")
	flags.StringVar(&config.MCPPort, "p", "8896", "MCP HTTP port")
	flags.StringVar(&config.WebSocketHost, "ws_host", "127.0.0.1", "Browser WebSocket host")
	flags.StringVar(&config.WebSocketPort, "ws_port", "8090", "Browser WebSocket port")
	flags.DurationVar(
		&config.CommandTimeout,
		"command_timeout",
		15*time.Second,
		"Default browser command timeout",
	)
	if err := flags.Parse(args); err != nil {
		return Config{}, fmt.Errorf("parse flags: %w", err)
	}
	if config.CommandTimeout <= 0 {
		return Config{}, fmt.Errorf("command_timeout must be positive")
	}
	switch config.Transport {
	case "streamable-http", "http", "stdio", "sse":
	default:
		return Config{}, fmt.Errorf("unsupported transport %q", config.Transport)
	}
	return config, nil
}

func run(
	ctx context.Context,
	config Config,
	stdin io.Reader,
	stdout io.Writer,
	logger *log.Logger,
) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	browserRegistry := registry.New()
	requestRouter := router.New(
		browserRegistry,
		router.WithDefaultTimeout(config.CommandTimeout),
		router.WithLogger(log.New(logger.Writer(), "[Router] ", log.LstdFlags)),
	)
	selections := selection.NewStore()

	hooks := &server.Hooks{}
	hooks.AddOnUnregisterSession(func(_ context.Context, session server.ClientSession) {
		selections.Delete(session.SessionID())
	})
	mcpServer := server.NewMCPServer(
		serverName,
		serverVersion,
		server.WithHooks(hooks),
		server.WithRecovery(),
		server.WithToolCapabilities(true),
	)
	browsertools.NewService(browserRegistry, requestRouter, selections).Register(mcpServer)

	websocketHandler := websockettransport.NewServer(
		browserRegistry,
		requestRouter,
		websockettransport.WithLogger(log.New(logger.Writer(), "[WebSocket] ", log.LstdFlags)),
	)
	websocketMux := http.NewServeMux()
	websocketMux.Handle(websockettransport.DefaultPath, websocketHandler)
	websocketHTTP := &http.Server{
		Addr:              net.JoinHostPort(config.WebSocketHost, config.WebSocketPort),
		Handler:           websocketMux,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       90 * time.Second,
	}
	websocketListener, err := net.Listen("tcp", websocketHTTP.Addr)
	if err != nil {
		return fmt.Errorf("listen for browser WebSocket connections on %s: %w", websocketHTTP.Addr, err)
	}

	errChannel := make(chan error, 2)
	go func() {
		logger.Printf(
			"Browser WebSocket server listening on ws://%s%s",
			websocketHTTP.Addr,
			websockettransport.DefaultPath,
		)
		errChannel <- normalizeServerError(websocketHTTP.Serve(websocketListener))
	}()

	var mcpHTTP *http.Server
	switch config.Transport {
	case "stdio":
		stdioServer := server.NewStdioServer(mcpServer)
		stdioServer.SetErrorLogger(log.New(logger.Writer(), "[MCP STDIO] ", log.LstdFlags))
		go func() {
			logger.Printf("MCP server using STDIO transport")
			errChannel <- stdioServer.Listen(runCtx, stdin, stdout)
		}()
	case "streamable-http", "http":
		sessionManager, managerErr := mcpsession.NewManager()
		if managerErr != nil {
			closeServer(websocketHTTP, logger)
			return managerErr
		}
		streamable := server.NewStreamableHTTPServer(
			mcpServer,
			server.WithSessionIdManager(sessionManager),
		)
		mux := http.NewServeMux()
		mux.Handle("/mcp", netguard.LocalOnly(streamable))
		mcpHTTP = newHTTPServer(config, mux)
		listener, listenErr := net.Listen("tcp", mcpHTTP.Addr)
		if listenErr != nil {
			closeServer(websocketHTTP, logger)
			return fmt.Errorf("listen for MCP connections on %s: %w", mcpHTTP.Addr, listenErr)
		}
		go func() {
			logger.Printf("MCP Streamable HTTP server listening on http://%s/mcp", mcpHTTP.Addr)
			errChannel <- normalizeServerError(mcpHTTP.Serve(listener))
		}()
	case "sse":
		baseURL := fmt.Sprintf("http://%s", net.JoinHostPort(config.MCPHost, config.MCPPort))
		sse := server.NewSSEServer(mcpServer, server.WithBaseURL(baseURL))
		mcpHTTP = newHTTPServer(config, netguard.LocalOnly(sse))
		listener, listenErr := net.Listen("tcp", mcpHTTP.Addr)
		if listenErr != nil {
			closeServer(websocketHTTP, logger)
			return fmt.Errorf("listen for legacy MCP SSE connections on %s: %w", mcpHTTP.Addr, listenErr)
		}
		go func() {
			logger.Printf("Legacy MCP SSE server listening on http://%s/sse", mcpHTTP.Addr)
			errChannel <- normalizeServerError(mcpHTTP.Serve(listener))
		}()
	}

	var runErr error
	select {
	case <-runCtx.Done():
		if !errors.Is(runCtx.Err(), context.Canceled) {
			runErr = runCtx.Err()
		}
	case serveErr := <-errChannel:
		runErr = serveErr
	}

	cancel()
	requestRouter.Close()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	if mcpHTTP != nil {
		if err := mcpHTTP.Shutdown(shutdownCtx); err != nil && runErr == nil {
			runErr = fmt.Errorf("shut down MCP HTTP server: %w", err)
		}
	}
	if err := websocketHTTP.Shutdown(shutdownCtx); err != nil && runErr == nil {
		runErr = fmt.Errorf("shut down WebSocket server: %w", err)
	}
	return runErr
}

func newHTTPServer(config Config, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              net.JoinHostPort(config.MCPHost, config.MCPPort),
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       90 * time.Second,
	}
}

func normalizeServerError(err error) error {
	if err == nil || errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func closeServer(server *http.Server, logger *log.Logger) {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Printf("failed to shut down server after startup error: %v", err)
	}
}
