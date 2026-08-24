package tools

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/hightemp/go_mcp_browser_ext_tool/internal/protocol"
	"github.com/mark3labs/mcp-go/mcp"
)

func TestSetEmulationHandlerPreservesRootTargetAndValidatesState(t *testing.T) {
	t.Parallel()

	service, connection, _ := newTestService(t)
	tabID := 7
	documentID := "document-1"
	locale := " en_US "
	timezoneID := " America/New_York "
	latency := 80.0
	download := 2_000.0
	maxTouchPoints := 5
	args := emulationSetArgs{
		BrowserID:  "browser-a",
		TabID:      &tabID,
		DocumentID: documentID,
		Viewport: &emulationViewport{
			Width: 390, Height: 844, DeviceScaleFactor: 3, Mobile: true,
			Orientation: " landscapePrimary ",
		},
		Touch:      &emulationTouch{Enabled: true, MaxTouchPoints: &maxTouchPoints},
		Network:    &emulationNetwork{LatencyMS: &latency, DownloadKbps: &download, ConnectionType: " cellular4g "},
		UserAgent:  &emulationUserAgent{Value: " ExampleBrowser/1.0 ", AcceptLanguage: " en-US ", Platform: " Linux armv8l "},
		Locale:     &locale,
		TimezoneID: &timezoneID,
		Geolocation: &emulationGeolocation{
			Latitude: 40.7, Longitude: -74, Accuracy: 20,
		},
		Media: &emulationMedia{Type: " SCREEN ", ColorScheme: " DARK "},
	}

	resultChannel := make(chan *mcp.CallToolResult, 1)
	go func() {
		result, _ := service.browserSetEmulationHandler(
			context.Background(), mcp.CallToolRequest{}, args,
		)
		resultChannel <- result
	}()

	request := receiveToolMessage(t, connection.messages)
	if request.Command != protocol.CommandEmulationSet || request.Target == nil ||
		request.Target.TabID == nil || *request.Target.TabID != tabID ||
		request.Target.FrameID == nil || *request.Target.FrameID != 0 ||
		request.Target.DocumentID != documentID {
		t.Fatalf("emulation request = %#v", request)
	}
	var config emulationConfig
	if err := json.Unmarshal(request.Params, &config); err != nil {
		t.Fatalf("decode emulation params: %v", err)
	}
	if config.Locale == nil || *config.Locale != "en_US" ||
		config.TimezoneID == nil || *config.TimezoneID != "America/New_York" ||
		config.Viewport.Orientation != "landscapePrimary" ||
		config.Network.ConnectionType != "cellular4g" || config.Media.ColorScheme != "dark" {
		t.Fatalf("normalized config = %#v", config)
	}
	wire := emulationWireResult{
		Active:        true,
		TabID:         tabID,
		DocumentID:    documentID,
		Settings:      &config,
		Applied:       emulationApplied(config),
		ResetOnDetach: true,
		Warnings:      []string{"Emulation remains active until reset or detach"},
	}
	response, err := protocol.NewResponse(request.RequestID, "browser-a", wire, nil)
	if err != nil {
		t.Fatalf("NewResponse() error = %v", err)
	}
	if !service.router.HandleResponse("browser-a", connection.ID(), response) {
		t.Fatal("HandleResponse() = false")
	}

	result := <-resultChannel
	if result.IsError {
		t.Fatalf("browserSetEmulationHandler() returned error: %s", toolText(t, result))
	}
	text := toolText(t, result)
	for _, expected := range []string{
		`"documentId":"document-1"`, `"timezoneId":"America/New_York"`,
		`"connectionType":"cellular4g"`, `"resetOnDetach":true`,
	} {
		if !strings.Contains(text, expected) {
			t.Errorf("tool result does not contain %s: %s", expected, text)
		}
	}
}

func TestNormalizeEmulationConfigRejectsUnsafeOrInvalidSettings(t *testing.T) {
	t.Parallel()

	validLocale := "en_US"
	invalidTimezone := "America/New York"
	tooManyTouchPoints := 11
	negativeLatency := -1.0
	for _, config := range []emulationConfig{
		{},
		{Viewport: &emulationViewport{Width: 0, Height: 800, DeviceScaleFactor: 2}},
		{Touch: &emulationTouch{Enabled: true, MaxTouchPoints: &tooManyTouchPoints}},
		{Network: &emulationNetwork{LatencyMS: &negativeLatency}},
		{UserAgent: &emulationUserAgent{Value: "Bad\nAgent"}},
		{Locale: &[]string{"not a locale"}[0]},
		{Locale: &validLocale, TimezoneID: &invalidTimezone},
		{Geolocation: &emulationGeolocation{Latitude: math.NaN(), Longitude: 0, Accuracy: 1}},
		{Media: &emulationMedia{}},
		{Media: &emulationMedia{ColorScheme: "sepia"}},
	} {
		if _, err := normalizeEmulationConfig(config); err == nil {
			t.Fatalf("normalizeEmulationConfig(%#v) error = nil", config)
		}
	}
}

func TestDecodeEmulationResultRejectsUnknownOrMismatchedState(t *testing.T) {
	t.Parallel()

	timezoneID := "UTC"
	config := emulationConfig{TimezoneID: &timezoneID}
	valid := emulationWireResult{
		Active:        true,
		TabID:         1,
		DocumentID:    "document-1",
		Settings:      &config,
		Applied:       []string{"timezoneId"},
		ResetOnDetach: true,
		Warnings:      []string{},
	}
	encode := func(value emulationWireResult) json.RawMessage {
		t.Helper()
		payload, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("json.Marshal() error = %v", err)
		}
		return payload
	}

	wrongApplied := valid
	wrongApplied.Applied = []string{"locale"}
	notReset := valid
	notReset.ResetOnDetach = false
	for _, raw := range []json.RawMessage{
		json.RawMessage(`{"active":true,"tabId":1,"documentId":"document-1","settings":{"timezoneId":"UTC","secret":"value"},"applied":["timezoneId"],"resetOnDetach":true,"warnings":[]}`),
		encode(wrongApplied),
		encode(notReset),
	} {
		if _, err := decodeEmulationResult(raw, protocol.CommandEmulationSet, &config); err == nil {
			t.Fatalf("decodeEmulationResult(%s) error = nil", raw)
		}
	}
}
