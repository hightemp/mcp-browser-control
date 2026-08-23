package app

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/hightemp/go_mcp_browser_ext_tool/internal/protocol"
)

const environmentPrefix = "MCP_BROWSER_"

// Config contains process-level server configuration.
type Config struct {
	ConfigFile                string
	Transport                 string
	MCPHost                   string
	MCPPort                   string
	WebSocketHost             string
	WebSocketPort             string
	CommandTimeout            time.Duration
	WebSocketHandshakeTimeout time.Duration
	WebSocketWriteTimeout     time.Duration
	WebSocketReadTimeout      time.Duration
	WebSocketPingInterval     time.Duration
	WebSocketSendQueueSize    int
	ShutdownTimeout           time.Duration
	WebSocketMaxMessageBytes  int64
	MCPMaxRequestBytes        int64
	CredentialFile            string
	PairingTTL                time.Duration
	PairingMaxAttempts        int
	PairingAttemptWindow      time.Duration
	OriginAllowlist           []string
	PermissionProfile         string
	ToolProfile               string
	ArtifactDirectory         string
	ArtifactTTL               time.Duration
	LogLevel                  string
	RedactLogs                bool
}

type fileConfig struct {
	Transport                 *string   `json:"transport"`
	MCPHost                   *string   `json:"mcpHost"`
	MCPPort                   *string   `json:"mcpPort"`
	WebSocketHost             *string   `json:"webSocketHost"`
	WebSocketPort             *string   `json:"webSocketPort"`
	CommandTimeout            *string   `json:"commandTimeout"`
	WebSocketHandshakeTimeout *string   `json:"webSocketHandshakeTimeout"`
	WebSocketWriteTimeout     *string   `json:"webSocketWriteTimeout"`
	WebSocketReadTimeout      *string   `json:"webSocketReadTimeout"`
	WebSocketPingInterval     *string   `json:"webSocketPingInterval"`
	WebSocketSendQueueSize    *int      `json:"webSocketSendQueueSize"`
	ShutdownTimeout           *string   `json:"shutdownTimeout"`
	WebSocketMaxMessageBytes  *int64    `json:"webSocketMaxMessageBytes"`
	MCPMaxRequestBytes        *int64    `json:"mcpMaxRequestBytes"`
	CredentialFile            *string   `json:"credentialFile"`
	PairingTTL                *string   `json:"pairingTTL"`
	PairingMaxAttempts        *int      `json:"pairingMaxAttempts"`
	PairingAttemptWindow      *string   `json:"pairingAttemptWindow"`
	OriginAllowlist           *[]string `json:"originAllowlist"`
	PermissionProfile         *string   `json:"permissionProfile"`
	ToolProfile               *string   `json:"toolProfile"`
	ArtifactDirectory         *string   `json:"artifactDirectory"`
	ArtifactTTL               *string   `json:"artifactTTL"`
	LogLevel                  *string   `json:"logLevel"`
	RedactLogs                *bool     `json:"redactLogs"`
}

func parseConfig(args []string, stderr io.Writer) (Config, error) {
	return parseConfigWithEnvironment(args, stderr, os.LookupEnv)
}

func parseConfigWithEnvironment(
	args []string,
	stderr io.Writer,
	lookupEnv func(string) (string, bool),
) (Config, error) {
	config := defaultConfig()
	config.ConfigFile = configPath(args, lookupEnv)
	if config.ConfigFile != "" {
		if err := applyConfigFile(&config, config.ConfigFile); err != nil {
			return Config{}, err
		}
	}
	if err := applyEnvironment(&config, lookupEnv); err != nil {
		return Config{}, err
	}
	if err := applyFlags(&config, args, stderr); err != nil {
		return Config{}, err
	}
	if err := config.Validate(); err != nil {
		return Config{}, err
	}
	return config, nil
}

func defaultConfig() Config {
	return Config{
		Transport:                 "streamable-http",
		MCPHost:                   "127.0.0.1",
		MCPPort:                   "8896",
		WebSocketHost:             "127.0.0.1",
		WebSocketPort:             "8090",
		CommandTimeout:            15 * time.Second,
		WebSocketHandshakeTimeout: 5 * time.Second,
		WebSocketWriteTimeout:     5 * time.Second,
		WebSocketReadTimeout:      60 * time.Second,
		WebSocketPingInterval:     20 * time.Second,
		WebSocketSendQueueSize:    64,
		ShutdownTimeout:           5 * time.Second,
		WebSocketMaxMessageBytes:  4 << 20,
		MCPMaxRequestBytes:        4 << 20,
		CredentialFile:            defaultCredentialFile(),
		PairingTTL:                10 * time.Minute,
		PairingMaxAttempts:        5,
		PairingAttemptWindow:      time.Minute,
		PermissionProfile:         "minimal",
		ToolProfile:               "standard",
		ArtifactDirectory:         defaultArtifactDirectory(),
		ArtifactTTL:               24 * time.Hour,
		LogLevel:                  "info",
		RedactLogs:                true,
	}
}

// Validate rejects unsafe listener addresses and invalid resource bounds.
func (c Config) Validate() error {
	switch c.Transport {
	case "streamable-http", "http", "stdio", "sse":
	default:
		return fmt.Errorf("unsupported transport %q", c.Transport)
	}
	if !isLoopbackHost(c.MCPHost) {
		return errors.New("mcp_host must be a loopback host")
	}
	if !isLoopbackHost(c.WebSocketHost) {
		return errors.New("ws_host must be a loopback host")
	}
	if err := validatePort("mcp_port", c.MCPPort); err != nil {
		return err
	}
	if err := validatePort("ws_port", c.WebSocketPort); err != nil {
		return err
	}
	if c.CommandTimeout <= 0 || c.CommandTimeout > time.Duration(protocol.MaxTimeoutMS)*time.Millisecond {
		return fmt.Errorf("command_timeout must be between 1ms and %dms", protocol.MaxTimeoutMS)
	}
	for name, value := range map[string]time.Duration{
		"ws_handshake_timeout": c.WebSocketHandshakeTimeout,
		"ws_write_timeout":     c.WebSocketWriteTimeout,
		"ws_read_timeout":      c.WebSocketReadTimeout,
		"ws_ping_interval":     c.WebSocketPingInterval,
		"shutdown_timeout":     c.ShutdownTimeout,
		"pairing_ttl":          c.PairingTTL,
		"pairing_window":       c.PairingAttemptWindow,
		"artifact_ttl":         c.ArtifactTTL,
	} {
		if value <= 0 {
			return fmt.Errorf("%s must be positive", name)
		}
	}
	if c.WebSocketMaxMessageBytes <= 0 || c.MCPMaxRequestBytes <= 0 {
		return errors.New("payload limits must be positive")
	}
	if c.WebSocketMaxMessageBytes > 64<<20 || c.MCPMaxRequestBytes > 64<<20 {
		return errors.New("payload limits must not exceed 67108864 bytes")
	}
	if c.PairingMaxAttempts <= 0 {
		return errors.New("pairing_max_attempts must be positive")
	}
	if c.WebSocketPingInterval >= c.WebSocketReadTimeout {
		return errors.New("ws_ping_interval must be shorter than ws_read_timeout")
	}
	if c.WebSocketSendQueueSize <= 0 || c.WebSocketSendQueueSize > 65536 {
		return errors.New("ws_send_queue_size must be between 1 and 65536")
	}
	if strings.TrimSpace(c.ArtifactDirectory) == "" {
		return errors.New("artifact_dir must not be empty")
	}
	if !oneOf(c.PermissionProfile, "minimal", "standard", "full") {
		return fmt.Errorf("unsupported permission_profile %q", c.PermissionProfile)
	}
	if !oneOf(c.ToolProfile, "minimal", "standard", "full") {
		return fmt.Errorf("unsupported tool_profile %q", c.ToolProfile)
	}
	if !oneOf(c.LogLevel, "error", "warn", "info", "debug") {
		return fmt.Errorf("unsupported log_level %q", c.LogLevel)
	}
	for _, origin := range c.OriginAllowlist {
		if err := validateAllowedOrigin(origin); err != nil {
			return err
		}
	}
	return nil
}

func applyFlags(config *Config, args []string, stderr io.Writer) error {
	flags := flag.NewFlagSet(serverName, flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&config.ConfigFile, "config", config.ConfigFile, "Path to a JSON configuration file")
	flags.StringVar(&config.Transport, "t", config.Transport, "Transport: streamable-http, stdio, or sse")
	flags.StringVar(&config.MCPHost, "h", config.MCPHost, "MCP HTTP host")
	flags.StringVar(&config.MCPPort, "p", config.MCPPort, "MCP HTTP port")
	flags.StringVar(&config.WebSocketHost, "ws_host", config.WebSocketHost, "Browser WebSocket host")
	flags.StringVar(&config.WebSocketPort, "ws_port", config.WebSocketPort, "Browser WebSocket port")
	flags.DurationVar(&config.CommandTimeout, "command_timeout", config.CommandTimeout, "Default browser command timeout")
	flags.DurationVar(&config.WebSocketHandshakeTimeout, "ws_handshake_timeout", config.WebSocketHandshakeTimeout, "Browser handshake timeout")
	flags.DurationVar(&config.WebSocketWriteTimeout, "ws_write_timeout", config.WebSocketWriteTimeout, "Browser write timeout")
	flags.DurationVar(&config.WebSocketReadTimeout, "ws_read_timeout", config.WebSocketReadTimeout, "Maximum interval without browser activity")
	flags.DurationVar(&config.WebSocketPingInterval, "ws_ping_interval", config.WebSocketPingInterval, "WebSocket control-ping interval")
	flags.IntVar(&config.WebSocketSendQueueSize, "ws_send_queue_size", config.WebSocketSendQueueSize, "Per-browser send queue capacity")
	flags.DurationVar(&config.ShutdownTimeout, "shutdown_timeout", config.ShutdownTimeout, "Graceful shutdown timeout")
	flags.Int64Var(&config.WebSocketMaxMessageBytes, "ws_max_message_bytes", config.WebSocketMaxMessageBytes, "Maximum browser message size")
	flags.Int64Var(&config.MCPMaxRequestBytes, "mcp_max_request_bytes", config.MCPMaxRequestBytes, "Maximum MCP HTTP request size")
	flags.StringVar(&config.CredentialFile, "credential_file", config.CredentialFile, "Persistent credential store; empty uses memory only")
	flags.DurationVar(&config.PairingTTL, "pairing_ttl", config.PairingTTL, "One-time pairing code lifetime")
	flags.IntVar(&config.PairingMaxAttempts, "pairing_max_attempts", config.PairingMaxAttempts, "Invalid pairing attempts per window")
	flags.DurationVar(&config.PairingAttemptWindow, "pairing_window", config.PairingAttemptWindow, "Pairing attempt rate-limit window")
	flags.Var(newStringListValue(&config.OriginAllowlist), "origin_allowlist", "Comma-separated exact allowed origins")
	flags.StringVar(&config.PermissionProfile, "permission_profile", config.PermissionProfile, "Permission profile: minimal, standard, or full")
	flags.StringVar(&config.ToolProfile, "tool_profile", config.ToolProfile, "Tool profile: minimal, standard, or full")
	flags.StringVar(&config.ArtifactDirectory, "artifact_dir", config.ArtifactDirectory, "Artifact storage directory")
	flags.DurationVar(&config.ArtifactTTL, "artifact_ttl", config.ArtifactTTL, "Artifact retention time")
	flags.StringVar(&config.LogLevel, "log_level", config.LogLevel, "Log level: error, warn, info, or debug")
	flags.BoolVar(&config.RedactLogs, "redact_logs", config.RedactLogs, "Redact sensitive values in logs")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("parse flags: %w", err)
	}
	return nil
}

func applyConfigFile(config *Config, path string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open config file: %w", err)
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var values fileConfig
	if err := decoder.Decode(&values); err != nil {
		return fmt.Errorf("decode config file: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return fmt.Errorf("decode config file: %w", err)
	}
	return values.apply(config)
}

func (f fileConfig) apply(config *Config) error {
	assignString(&config.Transport, f.Transport)
	assignString(&config.MCPHost, f.MCPHost)
	assignString(&config.MCPPort, f.MCPPort)
	assignString(&config.WebSocketHost, f.WebSocketHost)
	assignString(&config.WebSocketPort, f.WebSocketPort)
	assignString(&config.CredentialFile, f.CredentialFile)
	assignString(&config.PermissionProfile, f.PermissionProfile)
	assignString(&config.ToolProfile, f.ToolProfile)
	assignString(&config.ArtifactDirectory, f.ArtifactDirectory)
	assignString(&config.LogLevel, f.LogLevel)
	assignInt64(&config.WebSocketMaxMessageBytes, f.WebSocketMaxMessageBytes)
	assignInt64(&config.MCPMaxRequestBytes, f.MCPMaxRequestBytes)
	assignInt(&config.PairingMaxAttempts, f.PairingMaxAttempts)
	assignInt(&config.WebSocketSendQueueSize, f.WebSocketSendQueueSize)
	if f.RedactLogs != nil {
		config.RedactLogs = *f.RedactLogs
	}
	if f.OriginAllowlist != nil {
		config.OriginAllowlist = normalizeStringList(*f.OriginAllowlist)
	}
	for name, input := range map[string]struct {
		value *string
		dest  *time.Duration
	}{
		"commandTimeout":            {f.CommandTimeout, &config.CommandTimeout},
		"webSocketHandshakeTimeout": {f.WebSocketHandshakeTimeout, &config.WebSocketHandshakeTimeout},
		"webSocketWriteTimeout":     {f.WebSocketWriteTimeout, &config.WebSocketWriteTimeout},
		"webSocketReadTimeout":      {f.WebSocketReadTimeout, &config.WebSocketReadTimeout},
		"webSocketPingInterval":     {f.WebSocketPingInterval, &config.WebSocketPingInterval},
		"shutdownTimeout":           {f.ShutdownTimeout, &config.ShutdownTimeout},
		"pairingTTL":                {f.PairingTTL, &config.PairingTTL},
		"pairingAttemptWindow":      {f.PairingAttemptWindow, &config.PairingAttemptWindow},
		"artifactTTL":               {f.ArtifactTTL, &config.ArtifactTTL},
	} {
		if input.value == nil {
			continue
		}
		parsed, err := time.ParseDuration(*input.value)
		if err != nil {
			return fmt.Errorf("parse %s: %w", name, err)
		}
		*input.dest = parsed
	}
	return nil
}

func applyEnvironment(config *Config, lookupEnv func(string) (string, bool)) error {
	assignEnvString(lookupEnv, "TRANSPORT", &config.Transport)
	assignEnvString(lookupEnv, "MCP_HOST", &config.MCPHost)
	assignEnvString(lookupEnv, "MCP_PORT", &config.MCPPort)
	assignEnvString(lookupEnv, "WS_HOST", &config.WebSocketHost)
	assignEnvString(lookupEnv, "WS_PORT", &config.WebSocketPort)
	assignEnvString(lookupEnv, "CREDENTIAL_FILE", &config.CredentialFile)
	assignEnvString(lookupEnv, "PERMISSION_PROFILE", &config.PermissionProfile)
	assignEnvString(lookupEnv, "TOOL_PROFILE", &config.ToolProfile)
	assignEnvString(lookupEnv, "ARTIFACT_DIR", &config.ArtifactDirectory)
	assignEnvString(lookupEnv, "LOG_LEVEL", &config.LogLevel)
	if value, ok := lookupEnv(environmentPrefix + "ORIGIN_ALLOWLIST"); ok {
		config.OriginAllowlist = normalizeStringList(strings.Split(value, ","))
	}
	for name, destination := range map[string]*time.Duration{
		"COMMAND_TIMEOUT":      &config.CommandTimeout,
		"WS_HANDSHAKE_TIMEOUT": &config.WebSocketHandshakeTimeout,
		"WS_WRITE_TIMEOUT":     &config.WebSocketWriteTimeout,
		"WS_READ_TIMEOUT":      &config.WebSocketReadTimeout,
		"WS_PING_INTERVAL":     &config.WebSocketPingInterval,
		"SHUTDOWN_TIMEOUT":     &config.ShutdownTimeout,
		"PAIRING_TTL":          &config.PairingTTL,
		"PAIRING_WINDOW":       &config.PairingAttemptWindow,
		"ARTIFACT_TTL":         &config.ArtifactTTL,
	} {
		if err := assignEnvDuration(lookupEnv, name, destination); err != nil {
			return err
		}
	}
	if err := assignEnvInt64(lookupEnv, "WS_MAX_MESSAGE_BYTES", &config.WebSocketMaxMessageBytes); err != nil {
		return err
	}
	if err := assignEnvInt64(lookupEnv, "MCP_MAX_REQUEST_BYTES", &config.MCPMaxRequestBytes); err != nil {
		return err
	}
	if err := assignEnvInt(lookupEnv, "PAIRING_MAX_ATTEMPTS", &config.PairingMaxAttempts); err != nil {
		return err
	}
	if err := assignEnvInt(lookupEnv, "WS_SEND_QUEUE_SIZE", &config.WebSocketSendQueueSize); err != nil {
		return err
	}
	if value, ok := lookupEnv(environmentPrefix + "REDACT_LOGS"); ok {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("parse %sREDACT_LOGS: %w", environmentPrefix, err)
		}
		config.RedactLogs = parsed
	}
	return nil
}

func configPath(args []string, lookupEnv func(string) (string, bool)) string {
	path, _ := lookupEnv(environmentPrefix + "CONFIG")
	for index, argument := range args {
		if argument == "-config" || argument == "--config" {
			if index+1 < len(args) {
				path = args[index+1]
			}
			continue
		}
		if value, ok := strings.CutPrefix(argument, "-config="); ok {
			path = value
		}
		if value, ok := strings.CutPrefix(argument, "--config="); ok {
			path = value
		}
	}
	return strings.TrimSpace(path)
}

func defaultCredentialFile() string {
	configurationDirectory, err := os.UserConfigDir()
	if err != nil || configurationDirectory == "" {
		return filepath.Join(".mcp-browser-control", "credentials.json")
	}
	return filepath.Join(configurationDirectory, "mcp-browser-control", "credentials.json")
}

func defaultArtifactDirectory() string {
	cacheDirectory, err := os.UserCacheDir()
	if err != nil || cacheDirectory == "" {
		return filepath.Join(".mcp-browser-control", "artifacts")
	}
	return filepath.Join(cacheDirectory, "mcp-browser-control", "artifacts")
}

func validatePort(name, value string) error {
	port, err := strconv.Atoi(value)
	if err != nil || port < 0 || port > 65535 {
		return fmt.Errorf("%s must be an integer between 0 and 65535", name)
	}
	return nil
}

func isLoopbackHost(value string) bool {
	host := strings.Trim(strings.TrimSpace(value), "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func validateAllowedOrigin(origin string) error {
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("invalid allowed origin %q", origin)
	}
	switch parsed.Scheme {
	case "chrome-extension", "moz-extension":
		return nil
	case "http", "https":
		if isLoopbackHost(parsed.Hostname()) {
			return nil
		}
	}
	return fmt.Errorf("allowed origin %q must be a browser extension or loopback origin", origin)
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); errors.Is(err, io.EOF) {
		return nil
	} else if err != nil {
		return err
	}
	return errors.New("multiple JSON values are not allowed")
}

type stringListValue struct {
	destination *[]string
}

func newStringListValue(destination *[]string) *stringListValue {
	return &stringListValue{destination: destination}
}

func (v *stringListValue) String() string {
	if v == nil || v.destination == nil {
		return ""
	}
	return strings.Join(*v.destination, ",")
}

func (v *stringListValue) Set(value string) error {
	*v.destination = normalizeStringList(strings.Split(value, ","))
	return nil
}

func normalizeStringList(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func assignString(destination *string, source *string) {
	if source != nil {
		*destination = *source
	}
}

func assignInt(destination *int, source *int) {
	if source != nil {
		*destination = *source
	}
}

func assignInt64(destination *int64, source *int64) {
	if source != nil {
		*destination = *source
	}
}

func assignEnvString(lookupEnv func(string) (string, bool), name string, destination *string) {
	if value, ok := lookupEnv(environmentPrefix + name); ok {
		*destination = value
	}
}

func assignEnvDuration(
	lookupEnv func(string) (string, bool),
	name string,
	destination *time.Duration,
) error {
	value, ok := lookupEnv(environmentPrefix + name)
	if !ok {
		return nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fmt.Errorf("parse %s%s: %w", environmentPrefix, name, err)
	}
	*destination = parsed
	return nil
}

func assignEnvInt64(lookupEnv func(string) (string, bool), name string, destination *int64) error {
	value, ok := lookupEnv(environmentPrefix + name)
	if !ok {
		return nil
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return fmt.Errorf("parse %s%s: %w", environmentPrefix, name, err)
	}
	*destination = parsed
	return nil
}

func assignEnvInt(lookupEnv func(string) (string, bool), name string, destination *int) error {
	value, ok := lookupEnv(environmentPrefix + name)
	if !ok {
		return nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fmt.Errorf("parse %s%s: %w", environmentPrefix, name, err)
	}
	*destination = parsed
	return nil
}

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}
