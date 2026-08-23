package protocol

import (
	"errors"
	"math"
	"testing"
)

func TestResolveTarget(t *testing.T) {
	t.Parallel()

	tabID := 42
	original := &Target{TabID: &tabID}
	resolved, err := ResolveTarget("browser-a", original)
	if err != nil {
		t.Fatalf("ResolveTarget() error = %v", err)
	}
	if resolved.BrowserID != "browser-a" || resolved.TabID == nil || *resolved.TabID != tabID {
		t.Fatalf("ResolveTarget() = %#v", resolved)
	}
	if original.BrowserID != "" {
		t.Fatalf("ResolveTarget() mutated input: %#v", original)
	}

	_, err = ResolveTarget("browser-a", &Target{BrowserID: "browser-b", TabID: &tabID})
	assertErrorCode(t, err, CodeInvalidMessage)
}

func TestTargetValidate(t *testing.T) {
	t.Parallel()

	negative := -1
	frameID := 0
	tests := []struct {
		name   string
		target *Target
		code   ErrorCode
	}{
		{name: "browser", target: &Target{BrowserID: "browser-a"}},
		{name: "tab", target: &Target{BrowserID: "browser-a", TabID: intPointer(1)}},
		{name: "missing browser", target: &Target{TabID: intPointer(1)}, code: CodeInvalidMessage},
		{name: "negative id", target: &Target{BrowserID: "browser-a", WindowID: &negative}, code: CodeInvalidMessage},
		{name: "frame without tab", target: &Target{BrowserID: "browser-a", FrameID: &frameID}, code: CodeInvalidMessage},
		{name: "document without tab", target: &Target{BrowserID: "browser-a", DocumentID: "document-1"}, code: CodeInvalidMessage},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := test.target.Validate()
			if test.code == "" && err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
			if test.code != "" {
				assertErrorCode(t, err, test.code)
			}
		})
	}
}

func TestLocatorValidateStrategies(t *testing.T) {
	t.Parallel()

	strategies := map[string]Locator{
		"css":         {CSS: "#submit", IncludeShadowDOM: true},
		"xpath":       {XPath: "//button"},
		"text":        {Text: "Submit"},
		"role":        {Role: "button", Name: "Submit"},
		"label":       {Label: "Email"},
		"placeholder": {Placeholder: "name@example.com"},
		"alt":         {Alt: "Company logo"},
		"title":       {Title: "Close"},
		"test id":     {TestID: "submit"},
		"coordinates": {Coordinates: &Coordinates{X: 12.5, Y: 24}},
		"element": {
			Element: &ElementReference{ElementID: "element-1", DocumentID: "document-1"},
		},
	}
	for name, locator := range strategies {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if err := locator.Validate(nil); err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
		})
	}
}

func TestLocatorValidateRejectsInvalidShape(t *testing.T) {
	t.Parallel()

	negative := -1
	tests := []struct {
		name    string
		locator Locator
		code    ErrorCode
	}{
		{name: "no strategy", locator: Locator{}, code: CodeInvalidMessage},
		{name: "multiple strategies", locator: Locator{CSS: "a", Text: "link"}, code: CodeInvalidMessage},
		{name: "empty strategy", locator: Locator{CSS: "a", Text: " "}, code: CodeInvalidMessage},
		{name: "name without role", locator: Locator{CSS: "a", Name: "link"}, code: CodeInvalidMessage},
		{name: "negative nth", locator: Locator{CSS: "a", Nth: &negative}, code: CodeInvalidMessage},
		{name: "large nth", locator: Locator{CSS: "a", Nth: intPointer(MaxLocatorNth + 1)}, code: CodeInvalidMessage},
		{name: "negative coordinate", locator: Locator{Coordinates: &Coordinates{X: -1, Y: 0}}, code: CodeInvalidMessage},
		{name: "infinite coordinate", locator: Locator{Coordinates: &Coordinates{X: math.Inf(1), Y: 0}}, code: CodeInvalidMessage},
		{name: "incomplete element", locator: Locator{Element: &ElementReference{ElementID: "element-1"}}, code: CodeInvalidMessage},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			assertErrorCode(t, test.locator.Validate(nil), test.code)
		})
	}
}

func TestDocumentReferencesReturnStaleTarget(t *testing.T) {
	t.Parallel()

	tabID := 42
	target := &Target{BrowserID: "browser-a", TabID: &tabID, DocumentID: "document-1"}
	assertErrorCode(t, target.AssertDocument("document-2"), CodeStaleTarget)
	if err := target.AssertDocument("document-1"); err != nil {
		t.Fatalf("AssertDocument() error = %v", err)
	}

	reference := ElementReference{ElementID: "element-1", DocumentID: "document-1"}
	assertErrorCode(t, reference.AssertDocument("document-2"), CodeStaleTarget)
	locator := Locator{
		Element: &ElementReference{ElementID: "element-1", DocumentID: "document-2"},
	}
	assertErrorCode(t, locator.Validate(target), CodeStaleTarget)
}

func assertErrorCode(t *testing.T, err error, code ErrorCode) {
	t.Helper()
	var protocolErr *Error
	if !errors.As(err, &protocolErr) {
		t.Fatalf("error = %v, want *Error", err)
	}
	if protocolErr.Code != code {
		t.Fatalf("error code = %q, want %q", protocolErr.Code, code)
	}
}

func intPointer(value int) *int {
	return &value
}
