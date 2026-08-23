// Package selection stores browser choices scoped to MCP client sessions.
package selection

import (
	"strings"
	"sync"
	"time"

	"github.com/hightemp/go_mcp_browser_ext_tool/internal/protocol"
	"github.com/hightemp/go_mcp_browser_ext_tool/internal/registry"
)

// Selection is the current browser choice for an MCP session.
type Selection struct {
	BrowserID string    `json:"browserId"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Store is a concurrency-safe per-session selection store.
type Store struct {
	mu       sync.RWMutex
	sessions map[string]Selection
	now      func() time.Time
}

// NewStore creates an empty selection store.
func NewStore() *Store {
	return &Store{
		sessions: make(map[string]Selection),
		now:      time.Now,
	}
}

// Set selects browserID for sessionID.
func (s *Store) Set(sessionID, browserID string) error {
	if strings.TrimSpace(sessionID) == "" {
		return protocol.NewError(protocol.CodeInvalidMessage, "sessionId is required", false)
	}
	if strings.TrimSpace(browserID) == "" {
		return protocol.NewError(protocol.CodeInvalidMessage, "browserId is required", false)
	}

	s.mu.Lock()
	s.sessions[sessionID] = Selection{
		BrowserID: browserID,
		UpdatedAt: s.now().UTC(),
	}
	s.mu.Unlock()
	return nil
}

// Get returns the current selection for sessionID.
func (s *Store) Get(sessionID string) (Selection, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	selection, ok := s.sessions[sessionID]
	return selection, ok
}

// Delete removes session state.
func (s *Store) Delete(sessionID string) {
	s.mu.Lock()
	delete(s.sessions, sessionID)
	s.mu.Unlock()
}

// Resolve returns the explicit, selected, or unambiguous single connected
// browser according to the product selection rules.
func (s *Store) Resolve(sessionID, explicitBrowserID string, browsers []registry.Browser) (string, error) {
	if explicitBrowserID != "" {
		if browserConnected(explicitBrowserID, browsers) {
			return explicitBrowserID, nil
		}
		return "", protocol.NewError(protocol.CodeBrowserNotFound, "browser not found", false)
	}

	if selected, ok := s.Get(sessionID); ok {
		if browserConnected(selected.BrowserID, browsers) {
			return selected.BrowserID, nil
		}
		return "", protocol.NewError(protocol.CodeBrowserDisconnected, "the selected browser is disconnected", true)
	}

	switch len(browsers) {
	case 0:
		return "", protocol.NewError(protocol.CodeNoBrowserConnected, "no browser extensions are connected", true)
	case 1:
		return browsers[0].BrowserID, nil
	default:
		candidates := make([]map[string]string, 0, len(browsers))
		for _, browser := range browsers {
			candidates = append(candidates, map[string]string{
				"browserId":   browser.BrowserID,
				"displayName": browser.DisplayName,
			})
		}
		err := protocol.NewError(
			protocol.CodeAmbiguousBrowser,
			"multiple browsers are connected; provide browserId or call browser_select",
			false,
		)
		err.Details = map[string]any{"candidates": candidates}
		return "", err
	}
}

func browserConnected(browserID string, browsers []registry.Browser) bool {
	for _, browser := range browsers {
		if browser.BrowserID == browserID && browser.Connected {
			return true
		}
	}
	return false
}
