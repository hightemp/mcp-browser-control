// Package app assembles and runs the MCP browser control server.
package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/hightemp/go_mcp_browser_ext_tool/internal/artifacts"
	"github.com/hightemp/go_mcp_browser_ext_tool/internal/mcpsession"
	"github.com/hightemp/go_mcp_browser_ext_tool/internal/netguard"
	"github.com/hightemp/go_mcp_browser_ext_tool/internal/registry"
	"github.com/hightemp/go_mcp_browser_ext_tool/internal/router"
	"github.com/hightemp/go_mcp_browser_ext_tool/internal/security/pairing"
	securitytoken "github.com/hightemp/go_mcp_browser_ext_tool/internal/security/token"
	"github.com/hightemp/go_mcp_browser_ext_tool/internal/selection"
	browsertools "github.com/hightemp/go_mcp_browser_ext_tool/internal/tools"
	websockettransport "github.com/hightemp/go_mcp_browser_ext_tool/internal/transport/websocket"
	"github.com/mark3labs/mcp-go/server"
)

const (
	serverName    = "go_mcp_browser_ext_tool"
	serverVersion = "0.3.0"
)

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

func run(
	ctx context.Context,
	config Config,
	stdin io.Reader,
	stdout io.Writer,
	logger *log.Logger,
) error {
	if err := config.Validate(); err != nil {
		return fmt.Errorf("validate config: %w", err)
	}
	mcpToken := ""
	if config.Transport != "stdio" {
		var err error
		mcpToken, err = securitytoken.LoadOrCreate(config.MCPTokenFile)
		if err != nil {
			return fmt.Errorf("initialize MCP HTTP authentication: %w", err)
		}
		logger.Printf("MCP HTTP bearer authentication enabled; token file: %s", config.MCPTokenFile)
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	artifactStore, err := artifacts.New(
		config.ArtifactDirectory,
		config.ArtifactTTL,
		artifacts.WithMaxBytes(config.ArtifactMaxBytes),
	)
	if err != nil {
		return fmt.Errorf("initialize artifact store: %w", err)
	}
	authenticator, err := pairing.NewManager(
		pairing.WithStorePath(config.CredentialFile),
		pairing.WithCodeTTL(config.PairingTTL),
		pairing.WithAttemptLimit(config.PairingMaxAttempts, config.PairingAttemptWindow),
		pairing.WithCodeObserver(func(code string, expiresAt time.Time) {
			logger.Printf(
				"Browser pairing code: %s (expires at %s)",
				code,
				expiresAt.Format(time.RFC3339),
			)
		}),
	)
	if err != nil {
		return fmt.Errorf("initialize browser pairing: %w", err)
	}

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
	artifactStore.RegisterResources(mcpServer)

	websocketHandler := websockettransport.NewServer(
		browserRegistry,
		requestRouter,
		websockettransport.WithLogger(log.New(logger.Writer(), "[WebSocket] ", log.LstdFlags)),
		websockettransport.WithAuthenticator(authenticator),
		websockettransport.WithHandshakeTimeout(config.WebSocketHandshakeTimeout),
		websockettransport.WithWriteTimeout(config.WebSocketWriteTimeout),
		websockettransport.WithReadTimeout(config.WebSocketReadTimeout),
		websockettransport.WithPingInterval(config.WebSocketPingInterval),
		websockettransport.WithSendQueueSize(config.WebSocketSendQueueSize),
		websockettransport.WithMaxMessageBytes(config.WebSocketMaxMessageBytes),
		websockettransport.WithOriginAllowlist(config.OriginAllowlist),
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
	var serveGroup sync.WaitGroup
	startServer := func(serve func() error) {
		serveGroup.Add(1)
		go func() {
			defer serveGroup.Done()
			errChannel <- serve()
		}()
	}
	startServer(func() error {
		logger.Printf(
			"Browser WebSocket server listening on ws://%s%s",
			websocketHTTP.Addr,
			websockettransport.DefaultPath,
		)
		return normalizeServerError(websocketHTTP.Serve(websocketListener))
	})

	var mcpHTTP *http.Server
	switch config.Transport {
	case "stdio":
		stdioServer := server.NewStdioServer(mcpServer)
		stdioServer.SetErrorLogger(log.New(logger.Writer(), "[MCP STDIO] ", log.LstdFlags))
		startServer(func() error {
			logger.Printf("MCP server using STDIO transport")
			return stdioServer.Listen(runCtx, stdin, stdout)
		})
	case "streamable-http", "http":
		sessionManager, managerErr := mcpsession.NewManager(
			mcpsession.WithTerminationObserver(selections.Delete),
		)
		if managerErr != nil {
			closeServer(websocketHTTP, logger)
			return managerErr
		}
		streamable := server.NewStreamableHTTPServer(
			mcpServer,
			server.WithSessionIdManager(sessionManager),
		)
		mux := http.NewServeMux()
		mux.Handle("/mcp", guardedMCPHandler(config, mcpToken, streamable))
		mcpHTTP = newHTTPServer(config, mux)
		listener, listenErr := net.Listen("tcp", mcpHTTP.Addr)
		if listenErr != nil {
			closeServer(websocketHTTP, logger)
			return fmt.Errorf("listen for MCP connections on %s: %w", mcpHTTP.Addr, listenErr)
		}
		startServer(func() error {
			logger.Printf("MCP Streamable HTTP server listening on http://%s/mcp", mcpHTTP.Addr)
			return normalizeServerError(mcpHTTP.Serve(listener))
		})
	case "sse":
		logger.Printf("warning: legacy MCP SSE transport is deprecated")
		baseURL := fmt.Sprintf("http://%s", net.JoinHostPort(config.MCPHost, config.MCPPort))
		sse := server.NewSSEServer(mcpServer, server.WithBaseURL(baseURL))
		mcpHTTP = newHTTPServer(config, guardedMCPHandler(config, mcpToken, sse))
		listener, listenErr := net.Listen("tcp", mcpHTTP.Addr)
		if listenErr != nil {
			closeServer(websocketHTTP, logger)
			return fmt.Errorf("listen for legacy MCP SSE connections on %s: %w", mcpHTTP.Addr, listenErr)
		}
		startServer(func() error {
			logger.Printf("Legacy MCP SSE server listening on http://%s/sse", mcpHTTP.Addr)
			return normalizeServerError(mcpHTTP.Serve(listener))
		})
	}
	serveGroup.Add(1)
	go func() {
		defer serveGroup.Done()
		artifactStore.RunCleanup(runCtx)
	}()

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
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), config.ShutdownTimeout)
	defer shutdownCancel()

	if mcpHTTP != nil {
		if err := mcpHTTP.Shutdown(shutdownCtx); err != nil && runErr == nil {
			runErr = fmt.Errorf("shut down MCP HTTP server: %w", err)
		}
	}
	if err := websocketHTTP.Shutdown(shutdownCtx); err != nil && runErr == nil {
		runErr = fmt.Errorf("shut down WebSocket server: %w", err)
	}
	if err := websocketHandler.Shutdown(shutdownCtx); err != nil && runErr == nil {
		runErr = fmt.Errorf("close browser connections: %w", err)
	}
	serveDone := make(chan struct{})
	go func() {
		serveGroup.Wait()
		close(serveDone)
	}()
	select {
	case <-serveDone:
	case <-shutdownCtx.Done():
		if runErr == nil {
			runErr = fmt.Errorf("wait for server goroutines: %w", shutdownCtx.Err())
		}
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

func guardedMCPHandler(config Config, bearerToken string, handler http.Handler) http.Handler {
	authenticated := netguard.BearerToken(handler, bearerToken)
	local := netguard.LocalOnlyWithOrigins(authenticated, config.OriginAllowlist)
	return http.MaxBytesHandler(local, config.MCPMaxRequestBytes)
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
