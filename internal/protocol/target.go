package protocol

import (
	"fmt"
	"math"
	"strings"
)

const (
	// MaxTimeoutMS is the largest command timeout accepted by protocol v1.
	MaxTimeoutMS int64 = 120_000
	// MaxLocatorNth bounds indexed locator lookups.
	MaxLocatorNth = 10_000
	// MaxCoordinate bounds viewport coordinates before browser-side resolution.
	MaxCoordinate = 1_000_000
)

// Target identifies a browser object. Browser-local IDs are never valid
// without BrowserID.
type Target struct {
	BrowserID  string `json:"browserId"`
	WindowID   *int   `json:"windowId,omitempty"`
	TabID      *int   `json:"tabId,omitempty"`
	FrameID    *int   `json:"frameId,omitempty"`
	DocumentID string `json:"documentId,omitempty"`
}

// Coordinates identifies a point in viewport CSS pixels.
type Coordinates struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// ElementReference is a temporary element identity scoped to one document.
// The locator engine owns reference lifetime and expiry.
type ElementReference struct {
	ElementID  string `json:"elementId"`
	DocumentID string `json:"documentId"`
}

// Locator identifies an element with exactly one primary strategy. Name is a
// qualifier for Role; Nth and Strict are strategy modifiers.
type Locator struct {
	CSS              string            `json:"css,omitempty"`
	XPath            string            `json:"xpath,omitempty"`
	Text             string            `json:"text,omitempty"`
	Role             string            `json:"role,omitempty"`
	Name             string            `json:"name,omitempty"`
	Label            string            `json:"label,omitempty"`
	Placeholder      string            `json:"placeholder,omitempty"`
	Alt              string            `json:"alt,omitempty"`
	Title            string            `json:"title,omitempty"`
	TestID           string            `json:"testId,omitempty"`
	Coordinates      *Coordinates      `json:"coordinates,omitempty"`
	Element          *ElementReference `json:"element,omitempty"`
	Nth              *int              `json:"nth,omitempty"`
	Strict           *bool             `json:"strict,omitempty"`
	IncludeShadowDOM bool              `json:"includeShadowDOM,omitempty"`
}

// ResolveTarget returns an immutable copy associated with the resolved
// browser selection.
func ResolveTarget(browserID string, target *Target) (*Target, error) {
	if target == nil {
		return nil, nil
	}
	resolved := cloneTarget(target)
	if resolved.BrowserID == "" {
		resolved.BrowserID = browserID
	}
	if browserID == "" || resolved.BrowserID != browserID {
		return nil, NewError(
			CodeInvalidMessage,
			"target browserId must match the resolved browser",
			false,
		)
	}
	if err := resolved.Validate(); err != nil {
		return nil, err
	}
	return resolved, nil
}

// Validate checks target hierarchy and browser-local identifier bounds.
func (t *Target) Validate() error {
	if t == nil {
		return nil
	}
	if strings.TrimSpace(t.BrowserID) == "" {
		return NewError(CodeInvalidMessage, "target.browserId is required", false)
	}
	for name, value := range map[string]*int{
		"windowId": t.WindowID,
		"tabId":    t.TabID,
		"frameId":  t.FrameID,
	} {
		if value != nil && *value < 0 {
			return NewError(CodeInvalidMessage, fmt.Sprintf("target.%s must not be negative", name), false)
		}
	}
	if t.DocumentID != "" && strings.TrimSpace(t.DocumentID) == "" {
		return NewError(CodeInvalidMessage, "target.documentId must not be empty", false)
	}
	if t.TabID == nil && (t.FrameID != nil || t.DocumentID != "") {
		return NewError(CodeInvalidMessage, "target.frameId and documentId require tabId", false)
	}
	return nil
}

// AssertDocument returns STALE_TARGET when a document-scoped target no longer
// describes the current browser document.
func (t *Target) AssertDocument(actualDocumentID string) error {
	if t == nil || t.DocumentID == "" {
		return nil
	}
	return assertDocument(t.DocumentID, actualDocumentID)
}

// Validate checks locator shape, strategy count, modifiers, and document
// consistency with the target.
func (l *Locator) Validate(target *Target) error {
	if l == nil {
		return NewError(CodeInvalidMessage, "locator is required", false)
	}

	strategies := []string{
		l.CSS,
		l.XPath,
		l.Text,
		l.Role,
		l.Label,
		l.Placeholder,
		l.Alt,
		l.Title,
		l.TestID,
	}
	strategyCount := 0
	for _, strategy := range strategies {
		if strategy != "" && strings.TrimSpace(strategy) == "" {
			return NewError(CodeInvalidMessage, "locator strategy must not be empty", false)
		}
		if strings.TrimSpace(strategy) != "" {
			strategyCount++
		}
	}
	if l.Name != "" && strings.TrimSpace(l.Name) == "" {
		return NewError(CodeInvalidMessage, "locator.name must not be empty", false)
	}
	if l.Coordinates != nil {
		strategyCount++
	}
	if l.Element != nil {
		strategyCount++
	}
	if strategyCount != 1 {
		return NewError(CodeInvalidMessage, "locator must contain exactly one primary strategy", false)
	}
	if strings.TrimSpace(l.Name) != "" && strings.TrimSpace(l.Role) == "" {
		return NewError(CodeInvalidMessage, "locator.name requires locator.role", false)
	}
	if l.Nth != nil && (*l.Nth < 0 || *l.Nth > MaxLocatorNth) {
		return NewError(
			CodeInvalidMessage,
			fmt.Sprintf("locator.nth must be between 0 and %d", MaxLocatorNth),
			false,
		)
	}
	if l.Coordinates != nil {
		if err := l.Coordinates.Validate(); err != nil {
			return err
		}
	}
	if l.Element != nil {
		if err := l.Element.Validate(); err != nil {
			return err
		}
		if target != nil && target.DocumentID != "" {
			if err := assertDocument(l.Element.DocumentID, target.DocumentID); err != nil {
				return err
			}
		}
	}
	return nil
}

// Validate checks viewport coordinate bounds.
func (c Coordinates) Validate() error {
	if math.IsNaN(c.X) || math.IsInf(c.X, 0) || math.IsNaN(c.Y) || math.IsInf(c.Y, 0) ||
		c.X < 0 || c.X > MaxCoordinate || c.Y < 0 || c.Y > MaxCoordinate {
		return NewError(
			CodeInvalidMessage,
			fmt.Sprintf("locator coordinates must be between 0 and %d", MaxCoordinate),
			false,
		)
	}
	return nil
}

// Validate checks that an element reference has both scoped identities.
func (r ElementReference) Validate() error {
	if strings.TrimSpace(r.ElementID) == "" || strings.TrimSpace(r.DocumentID) == "" {
		return NewError(
			CodeInvalidMessage,
			"element reference requires elementId and documentId",
			false,
		)
	}
	return nil
}

// AssertDocument returns STALE_TARGET for a reference from another document.
func (r ElementReference) AssertDocument(actualDocumentID string) error {
	if err := r.Validate(); err != nil {
		return err
	}
	return assertDocument(r.DocumentID, actualDocumentID)
}

func assertDocument(expectedDocumentID, actualDocumentID string) error {
	if expectedDocumentID == actualDocumentID && actualDocumentID != "" {
		return nil
	}
	return &Error{
		Code:      CodeStaleTarget,
		Message:   "the referenced document is no longer current",
		Retryable: false,
		Details:   map[string]any{"expectedDocumentId": expectedDocumentID},
	}
}
