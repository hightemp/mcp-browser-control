//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	gorilla "github.com/gorilla/websocket"
)

const chromeLogLimit = 64 << 10

type chromeInstance struct {
	command     *exec.Cmd
	processDone chan error
	logs        *boundedBuffer
	cdp         *cdpClient
	profileDir  string
	stopOnce    sync.Once
}

type cdpClient struct {
	socket *gorilla.Conn
	mu     sync.Mutex
	nextID int64
}

type cdpTarget struct {
	TargetID string `json:"targetId"`
	Type     string `json:"type"`
	Title    string `json:"title"`
	URL      string `json:"url"`
}

type cdpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type cdpEnvelope struct {
	ID        int64           `json:"id"`
	SessionID string          `json:"sessionId,omitempty"`
	Method    string          `json:"method,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     *cdpError       `json:"error,omitempty"`
}

type boundedBuffer struct {
	mu   sync.Mutex
	data []byte
}

func launchChrome(
	t *testing.T,
	binary string,
	extensionDirectory string,
) *chromeInstance {
	t.Helper()

	profileDirectory := t.TempDir()
	logs := &boundedBuffer{}
	command := exec.Command(
		binary,
		"--headless=new",
		"--no-sandbox",
		"--disable-gpu",
		"--disable-dev-shm-usage",
		"--disable-background-networking",
		"--disable-component-update",
		"--disable-default-apps",
		"--no-default-browser-check",
		"--no-first-run",
		"--remote-debugging-address=127.0.0.1",
		"--remote-debugging-port=0",
		"--user-data-dir="+profileDirectory,
		"--disable-extensions-except="+extensionDirectory,
		"--load-extension="+extensionDirectory,
		"about:blank",
	)
	command.Stdout = logs
	command.Stderr = logs
	if err := command.Start(); err != nil {
		t.Fatalf("start Chrome: %v", err)
	}
	instance := &chromeInstance{
		command:     command,
		processDone: make(chan error, 1),
		logs:        logs,
		profileDir:  profileDirectory,
	}
	go func() { instance.processDone <- command.Wait() }()
	t.Cleanup(instance.stop)

	debuggerURL, err := instance.waitForDebuggerURL(10 * time.Second)
	if err != nil {
		t.Fatalf("start Chrome DevTools: %v\nChrome output:\n%s", err, logs.String())
	}
	socket, response, err := gorilla.DefaultDialer.Dial(debuggerURL, nil)
	if err != nil {
		if response != nil {
			t.Fatalf("connect Chrome DevTools: %v (%s)", err, response.Status)
		}
		t.Fatalf("connect Chrome DevTools: %v", err)
	}
	instance.cdp = &cdpClient{socket: socket}
	return instance
}

func (c *chromeInstance) waitForDebuggerURL(timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	portFile := filepath.Join(c.profileDir, "DevToolsActivePort")
	for time.Now().Before(deadline) {
		select {
		case err := <-c.processDone:
			return "", fmt.Errorf("Chrome exited before DevTools became ready: %w", err)
		default:
		}
		payload, err := os.ReadFile(portFile)
		if err == nil {
			lines := strings.Split(strings.TrimSpace(string(payload)), "\n")
			if len(lines) >= 2 {
				port, parseErr := strconv.Atoi(strings.TrimSpace(lines[0]))
				if parseErr == nil && port > 0 && strings.HasPrefix(lines[1], "/devtools/browser/") {
					return fmt.Sprintf("ws://127.0.0.1:%d%s", port, strings.TrimSpace(lines[1])), nil
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	return "", errors.New("timed out waiting for DevToolsActivePort")
}

func (c *chromeInstance) stop() {
	c.stopOnce.Do(func() {
		if c.cdp != nil {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			_ = c.cdp.call(ctx, "", "Browser.close", nil, nil)
			cancel()
			_ = c.cdp.socket.Close()
		}
		select {
		case <-c.processDone:
			return
		case <-time.After(2 * time.Second):
		}
		if c.command.Process != nil {
			_ = c.command.Process.Kill()
		}
		select {
		case <-c.processDone:
		case <-time.After(time.Second):
		}
	})
}

func (c *chromeInstance) waitForServiceWorker(
	ctx context.Context,
	excludedTargetID string,
) (cdpTarget, string, error) {
	for {
		var result struct {
			TargetInfos []cdpTarget `json:"targetInfos"`
		}
		if err := c.cdp.call(ctx, "", "Target.getTargets", nil, &result); err != nil {
			return cdpTarget{}, "", err
		}
		for _, target := range result.TargetInfos {
			if target.Type != "service_worker" || target.TargetID == excludedTargetID {
				continue
			}
			parsed, err := url.Parse(target.URL)
			if err == nil && parsed.Scheme == "chrome-extension" &&
				strings.HasSuffix(parsed.Path, "/src/service-worker.js") {
				return target, parsed.Host, nil
			}
		}
		select {
		case <-ctx.Done():
			return cdpTarget{}, "", ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
}

func (c *chromeInstance) attach(ctx context.Context, targetID string) (string, error) {
	var result struct {
		SessionID string `json:"sessionId"`
	}
	err := c.cdp.call(ctx, "", "Target.attachToTarget", map[string]any{
		"targetId": targetID,
		"flatten":  true,
	}, &result)
	if err != nil {
		return "", err
	}
	if result.SessionID == "" {
		return "", errors.New("Chrome returned an empty CDP session ID")
	}
	return result.SessionID, nil
}

func (c *chromeInstance) createTarget(ctx context.Context, targetURL string) (string, error) {
	var result struct {
		TargetID string `json:"targetId"`
	}
	err := c.cdp.call(ctx, "", "Target.createTarget", map[string]any{"url": targetURL}, &result)
	return result.TargetID, err
}

func (c *chromeInstance) closeTarget(ctx context.Context, targetID string) error {
	var result struct {
		Success bool `json:"success"`
	}
	if err := c.cdp.call(
		ctx,
		"",
		"Target.closeTarget",
		map[string]any{"targetId": targetID},
		&result,
	); err != nil {
		return err
	}
	if !result.Success {
		return fmt.Errorf("Chrome did not close target %s", targetID)
	}
	return nil
}

func (c *chromeInstance) runtimeMessage(
	ctx context.Context,
	sessionID string,
	message map[string]any,
) (map[string]any, error) {
	payload, err := json.Marshal(message)
	if err != nil {
		return nil, err
	}
	value, err := c.evaluate(
		ctx,
		sessionID,
		"chrome.runtime.sendMessage("+string(payload)+")",
		true,
	)
	if err != nil {
		return nil, err
	}
	var response map[string]any
	if err := json.Unmarshal(value, &response); err != nil {
		return nil, fmt.Errorf("decode extension runtime response: %w", err)
	}
	return response, nil
}

func (c *chromeInstance) waitForExtensionRuntime(
	ctx context.Context,
	sessionID string,
) error {
	var lastErr error
	for {
		value, err := c.evaluate(
			ctx,
			sessionID,
			`location.protocol === "chrome-extension:" && typeof chrome?.runtime?.sendMessage === "function"`,
			false,
		)
		if err == nil {
			var ready bool
			if decodeErr := json.Unmarshal(value, &ready); decodeErr == nil && ready {
				return nil
			}
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("extension runtime was not ready: %w (last error: %v)", ctx.Err(), lastErr)
		case <-time.After(25 * time.Millisecond):
		}
	}
}

func (c *chromeInstance) evaluate(
	ctx context.Context,
	sessionID string,
	expression string,
	userGesture bool,
) (json.RawMessage, error) {
	var result struct {
		Result struct {
			Type        string          `json:"type"`
			Value       json.RawMessage `json:"value"`
			Description string          `json:"description"`
		} `json:"result"`
		ExceptionDetails *struct {
			Text      string `json:"text"`
			Exception struct {
				Description string `json:"description"`
			} `json:"exception"`
		} `json:"exceptionDetails,omitempty"`
	}
	if err := c.cdp.call(ctx, sessionID, "Runtime.evaluate", map[string]any{
		"expression":    expression,
		"awaitPromise":  true,
		"returnByValue": true,
		"userGesture":   userGesture,
	}, &result); err != nil {
		return nil, err
	}
	if result.ExceptionDetails != nil {
		description := strings.TrimSpace(result.ExceptionDetails.Exception.Description)
		if description == "" {
			description = result.ExceptionDetails.Text
		}
		return nil, fmt.Errorf("extension evaluation failed: %s", description)
	}
	if result.Result.Type == "undefined" || len(result.Result.Value) == 0 {
		return nil, errors.New("extension evaluation returned no value")
	}
	return result.Result.Value, nil
}

func (c *cdpClient) call(
	ctx context.Context,
	sessionID string,
	method string,
	params any,
	result any,
) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.nextID++
	requestID := c.nextID
	request := map[string]any{
		"id":     requestID,
		"method": method,
	}
	if sessionID != "" {
		request["sessionId"] = sessionID
	}
	if params != nil {
		request["params"] = params
	}
	deadline := time.Now().Add(10 * time.Second)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	if err := c.socket.SetWriteDeadline(deadline); err != nil {
		return err
	}
	if err := c.socket.WriteJSON(request); err != nil {
		return fmt.Errorf("send CDP %s: %w", method, err)
	}
	if err := c.socket.SetReadDeadline(deadline); err != nil {
		return err
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		var response cdpEnvelope
		if err := c.socket.ReadJSON(&response); err != nil {
			return fmt.Errorf("read CDP %s: %w", method, err)
		}
		if response.ID != requestID {
			continue
		}
		if response.Error != nil {
			return fmt.Errorf(
				"CDP %s failed (%d): %s",
				method,
				response.Error.Code,
				response.Error.Message,
			)
		}
		if result != nil && len(response.Result) > 0 {
			if err := json.Unmarshal(response.Result, result); err != nil {
				return fmt.Errorf("decode CDP %s result: %w", method, err)
			}
		}
		return nil
	}
}

func (b *boundedBuffer) Write(payload []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.data = append(b.data, payload...)
	if len(b.data) > chromeLogLimit {
		b.data = append([]byte(nil), b.data[len(b.data)-chromeLogLimit:]...)
	}
	return len(payload), nil
}

func (b *boundedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(append([]byte(nil), b.data...))
}

var _ io.Writer = (*boundedBuffer)(nil)
