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
	BrowserID  string                  `json:"browserId,omitempty"`
	UpdatedAt  time.Time               `json:"updatedAt,omitempty"`
	LastUsedAt time.Time               `json:"lastUsedAt,omitempty"`
	Tabs       map[string]TabSelection `json:"tabs,omitempty"`
}

// TabSelection is a browser-scoped default tab for one MCP session.
type TabSelection struct {
	BrowserID  string    `json:"browserId"`
	TabID      int       `json:"tabId"`
	UpdatedAt  time.Time `json:"updatedAt"`
	LastUsedAt time.Time `json:"lastUsedAt"`
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
	selection := s.sessions[sessionID]
	now := s.now().UTC()
	selection.BrowserID = browserID
	selection.UpdatedAt = now
	selection.LastUsedAt = now
	if selection.Tabs == nil {
		selection.Tabs = make(map[string]TabSelection)
	}
	s.sessions[sessionID] = selection
	s.mu.Unlock()
	return nil
}

// SetTab selects a default tab scoped to browserID and sessionID.
func (s *Store) SetTab(sessionID, browserID string, tabID int) error {
	if strings.TrimSpace(sessionID) == "" {
		return protocol.NewError(protocol.CodeInvalidMessage, "sessionId is required", false)
	}
	if strings.TrimSpace(browserID) == "" {
		return protocol.NewError(protocol.CodeInvalidMessage, "browserId is required", false)
	}
	if tabID < 0 {
		return protocol.NewError(protocol.CodeInvalidMessage, "tabId must not be negative", false)
	}

	s.mu.Lock()
	selection := s.sessions[sessionID]
	if selection.Tabs == nil {
		selection.Tabs = make(map[string]TabSelection)
	}
	now := s.now().UTC()
	selection.Tabs[browserID] = TabSelection{
		BrowserID:  browserID,
		TabID:      tabID,
		UpdatedAt:  now,
		LastUsedAt: now,
	}
	if selection.BrowserID == browserID {
		selection.LastUsedAt = now
	}
	s.sessions[sessionID] = selection
	s.mu.Unlock()
	return nil
}

// Get returns the current selection for sessionID.
func (s *Store) Get(sessionID string) (Selection, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	selection, ok := s.sessions[sessionID]
	return cloneSelection(selection), ok
}

// GetTab returns a browser-scoped tab selection without updating usage time.
func (s *Store) GetTab(sessionID, browserID string) (TabSelection, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	selection, ok := s.sessions[sessionID]
	if !ok {
		return TabSelection{}, false
	}
	tab, ok := selection.Tabs[browserID]
	return tab, ok
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
			s.touchBrowser(sessionID, explicitBrowserID)
			return explicitBrowserID, nil
		}
		return "", protocol.NewError(protocol.CodeBrowserNotFound, "browser not found", false)
	}

	if selected, ok := s.Get(sessionID); ok && selected.BrowserID != "" {
		if browserConnected(selected.BrowserID, browsers) {
			s.touchBrowser(sessionID, selected.BrowserID)
			return selected.BrowserID, nil
		}
		return "", protocol.NewError(protocol.CodeBrowserDisconnected, "the selected browser is disconnected", true)
	}

	switch len(browsers) {
	case 0:
		return "", protocol.NewError(protocol.CodeNoBrowserConnected, "no browser extensions are connected", true)
	case 1:
		s.touchBrowser(sessionID, browsers[0].BrowserID)
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

// ResolveTab gives an explicit tab priority, otherwise returning the saved tab
// for the resolved browser. Returned pointers are independent snapshots.
func (s *Store) ResolveTab(sessionID, browserID string, explicitTabID *int) (*int, error) {
	if explicitTabID != nil {
		if *explicitTabID < 0 {
			return nil, protocol.NewError(protocol.CodeInvalidMessage, "tabId must not be negative", false)
		}
		s.touchTab(sessionID, browserID, *explicitTabID)
		value := *explicitTabID
		return &value, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	selection, ok := s.sessions[sessionID]
	if !ok {
		return nil, nil
	}
	tab, ok := selection.Tabs[browserID]
	if !ok {
		return nil, nil
	}
	now := s.now().UTC()
	tab.LastUsedAt = now
	selection.Tabs[browserID] = tab
	if selection.BrowserID == browserID {
		selection.LastUsedAt = now
	}
	s.sessions[sessionID] = selection
	value := tab.TabID
	return &value, nil
}

func (s *Store) touchBrowser(sessionID, browserID string) {
	s.mu.Lock()
	selection, ok := s.sessions[sessionID]
	if ok && selection.BrowserID == browserID {
		selection.LastUsedAt = s.now().UTC()
		s.sessions[sessionID] = selection
	}
	s.mu.Unlock()
}

func (s *Store) touchTab(sessionID, browserID string, tabID int) {
	s.mu.Lock()
	selection, ok := s.sessions[sessionID]
	if ok {
		if tab, selected := selection.Tabs[browserID]; selected && tab.TabID == tabID {
			now := s.now().UTC()
			tab.LastUsedAt = now
			selection.Tabs[browserID] = tab
			if selection.BrowserID == browserID {
				selection.LastUsedAt = now
			}
			s.sessions[sessionID] = selection
		}
	}
	s.mu.Unlock()
}

func cloneSelection(selection Selection) Selection {
	if selection.Tabs == nil {
		return selection
	}
	selection.Tabs = make(map[string]TabSelection, len(selection.Tabs))
	for browserID, tab := range selection.Tabs {
		selection.Tabs[browserID] = tab
	}
	return selection
}

func browserConnected(browserID string, browsers []registry.Browser) bool {
	for _, browser := range browsers {
		if browser.BrowserID == browserID && browser.Connected {
			return true
		}
	}
	return false
}
