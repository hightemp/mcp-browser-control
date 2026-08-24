// Package policy enforces server-owned browser action boundaries.
package policy

import (
	"fmt"
	"log"
	"net"
	"net/url"
	"strings"

	"github.com/hightemp/go_mcp_browser_ext_tool/internal/protocol"
)

// Action controls which page origins and browser contexts commands may use.
type Action struct {
	allowOrigins   map[string]struct{}
	denyOrigins    map[string]struct{}
	allowIncognito bool
	logger         *log.Logger
}

// NewAction creates an immutable action policy. Origin lists contain exact
// HTTP(S) origins; deny entries take precedence over allow entries.
func NewAction(
	allowOrigins []string,
	denyOrigins []string,
	allowIncognito bool,
	logger *log.Logger,
) (*Action, error) {
	policy := &Action{
		allowOrigins:   make(map[string]struct{}, len(allowOrigins)),
		denyOrigins:    make(map[string]struct{}, len(denyOrigins)),
		allowIncognito: allowIncognito,
		logger:         logger,
	}
	for _, item := range []struct {
		name   string
		values []string
		target map[string]struct{}
	}{
		{name: "page origin allowlist", values: allowOrigins, target: policy.allowOrigins},
		{name: "page origin denylist", values: denyOrigins, target: policy.denyOrigins},
	} {
		for _, value := range item.values {
			origin, err := NormalizeOrigin(value)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", item.name, err)
			}
			item.target[origin] = struct{}{}
		}
	}
	return policy, nil
}

// NormalizeOrigin validates and canonicalizes an exact HTTP(S) origin.
func NormalizeOrigin(value string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("invalid page origin %q", value)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("page origin %q must use http or https", value)
	}
	if parsed.User != nil || (parsed.Path != "" && parsed.Path != "/") ||
		parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("page origin %q must not contain credentials, a path, query, or fragment", value)
	}
	if parsed.Hostname() == "" {
		return "", fmt.Errorf("invalid page origin %q", value)
	}
	host := strings.ToLower(parsed.Hostname())
	port := parsed.Port()
	if (parsed.Scheme == "http" && port == "80") || (parsed.Scheme == "https" && port == "443") {
		port = ""
	}
	if port != "" {
		host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return (&url.URL{Scheme: strings.ToLower(parsed.Scheme), Host: host}).String(), nil
}

// CheckURL denies restricted schemes, browser stores, denylisted origins, and
// origins outside a configured allowlist.
func (p *Action) CheckURL(action, browserID, rawURL string) *protocol.Error {
	parsed, origin, reason := inspectURL(rawURL)
	if reason == "" {
		_, denied := p.denyOrigins[origin]
		_, allowed := p.allowOrigins[origin]
		switch {
		case denied:
			reason = "origin_denylist"
		case len(p.allowOrigins) > 0 && !allowed:
			reason = "origin_not_allowlisted"
		}
	}
	if reason == "" {
		return nil
	}
	p.AuditDenied(action, browserID, origin, reason)
	message := "the target URL is denied by server action policy"
	if parsed != nil && parsed.Scheme != "http" && parsed.Scheme != "https" {
		message = "the target URL uses a restricted scheme"
	}
	return protocol.NewError(protocol.CodeRestrictedURL, message, false)
}

// CheckIncognito denies actions in incognito contexts unless explicitly
// enabled by server configuration.
func (p *Action) CheckIncognito(action, browserID string, incognito bool) *protocol.Error {
	if !incognito || p.allowIncognito {
		return nil
	}
	p.AuditDenied(action, browserID, "", "incognito_disabled")
	return protocol.NewError(
		protocol.CodeRestrictedURL,
		"incognito browser contexts are disabled by server action policy",
		false,
	)
}

// AllowsIncognito reports whether browser-wide handlers may include incognito
// state in their internally constructed extension commands.
func (p *Action) AllowsIncognito() bool {
	return p != nil && p.allowIncognito
}

// AuditDenied records only bounded action metadata and never logs a full URL,
// query, command arguments, or browser result.
func (p *Action) AuditDenied(action, browserID, origin, reason string) {
	if p == nil || p.logger == nil {
		return
	}
	p.logger.Printf(
		"denied action=%s browserId=%s origin=%s reason=%s",
		boundedField(action),
		boundedField(browserID),
		boundedField(origin),
		boundedField(reason),
	)
}

func inspectURL(rawURL string) (*url.URL, string, string) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil {
		return parsed, "", "invalid_url"
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return parsed, "", "restricted_scheme"
	}
	origin, err := NormalizeOrigin((&url.URL{Scheme: parsed.Scheme, Host: parsed.Host}).String())
	if err != nil {
		return parsed, "", "invalid_url"
	}
	host := strings.ToLower(parsed.Hostname())
	path := strings.ToLower(parsed.EscapedPath())
	if host == "chromewebstore.google.com" ||
		(host == "chrome.google.com" && strings.HasPrefix(path, "/webstore")) ||
		(host == "microsoftedge.microsoft.com" && strings.HasPrefix(path, "/addons")) {
		return parsed, origin, "browser_store"
	}
	return parsed, origin, ""
}

func boundedField(value string) string {
	value = strings.Map(func(character rune) rune {
		if character < 0x20 || character == 0x7f {
			return -1
		}
		return character
	}, value)
	const maxLength = 256
	characters := []rune(value)
	if len(characters) > maxLength {
		return string(characters[:maxLength])
	}
	return value
}
