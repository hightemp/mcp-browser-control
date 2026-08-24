// Package redaction removes sensitive browser data and bounds JSON returned to
// MCP clients.
package redaction

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	redactedValue  = "[REDACTED]"
	redactedPath   = "[REDACTED_PATH]"
	truncatedValue = "[TRUNCATED]"
)

var (
	// ErrInputTooLarge indicates that a browser JSON document exceeded its
	// pre-parse bound.
	ErrInputTooLarge = errors.New("redaction input exceeds the configured limit")
	// ErrOutputTooLarge indicates that sanitized JSON still exceeded the MCP
	// result bound.
	ErrOutputTooLarge = errors.New("redaction output exceeds the configured limit")

	urlUserInfoPattern   = regexp.MustCompile(`(?i)(https?://)[^/@\s:]+:[^/@\s]+@`)
	bearerPattern        = regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/=-]+`)
	authorizationPattern = regexp.MustCompile(
		`(?i)\b((?:proxy-)?authorization\s*:\s*)(?:basic|bearer)?\s*[^\r\n,;]+`,
	)
	cookieHeaderPattern = regexp.MustCompile(`(?i)\b((?:set-)?cookie\s*:\s*)[^\r\n]+`)
	querySecretPattern  = regexp.MustCompile(
		`(?i)([?&#](?:password|passwd|passphrase|secret|token|credential|authorization|cookie|api[-_]?key|access[-_]?token|refresh[-_]?token)=)[^&#\s]*`,
	)
	keyValueSecretPattern = regexp.MustCompile(
		`(?i)(\b(?:password|passwd|passphrase|secret|token|credential|authorization|cookie|api[-_]?key|access[-_]?token|refresh[-_]?token)\b\s*[:=]\s*)[^,;\s&]+`,
	)
	localPathPattern = regexp.MustCompile(
		`(?i)(?:[A-Z]:\\(?:[^\\\s"']+\\)*[^\\\s"']*|\\\\[^\\\s"']+\\[^\\\s"']+(?:\\[^\\\s"']+)*|/(?:home|users|tmp|var/tmp|private|mnt|volumes)(?:/[^\s"']*)?)`,
	)
	sensitiveIdentityPattern = regexp.MustCompile(
		`(?i)(?:password|passwd|passphrase|secret|token|credential|authorization|cookie|api[-_ ]?key|access[-_ ]?token|refresh[-_ ]?token)`,
	)
)

// Limits bounds redaction work and the sanitized JSON document.
type Limits struct {
	MaxInputBytes  int
	MaxOutputBytes int
	MaxStringBytes int
	MaxDepth       int
	MaxNodes       int
}

// DefaultLimits returns conservative bounds for one browser result.
func DefaultLimits(maxOutputBytes int) Limits {
	return Limits{
		MaxInputBytes:  64 << 20,
		MaxOutputBytes: maxOutputBytes,
		MaxStringBytes: 1_000_000,
		MaxDepth:       64,
		MaxNodes:       100_000,
	}
}

// Validate rejects disabled or impractically large redaction limits.
func (l Limits) Validate() error {
	if l.MaxInputBytes <= 0 || l.MaxOutputBytes <= 0 || l.MaxStringBytes <= 0 ||
		l.MaxDepth <= 0 || l.MaxNodes <= 0 {
		return errors.New("redaction limits must be positive")
	}
	if l.MaxInputBytes > 64<<20 || l.MaxOutputBytes > 64<<20 ||
		l.MaxStringBytes > 64<<20 || l.MaxDepth > 256 || l.MaxNodes > 1_000_000 {
		return errors.New("redaction limits exceed the supported maximum")
	}
	return nil
}

// Report describes server-side redaction and truncation without retaining any
// original values.
type Report struct {
	Applied   bool
	Truncated bool
	Rules     []string
}

// Warnings returns stable user-facing summaries for an MCP result envelope.
func (r Report) Warnings() []string {
	warnings := make([]string, 0, 2)
	if r.Applied {
		warnings = append(warnings, "Sensitive browser data was redacted by the server")
	}
	if r.Truncated {
		warnings = append(warnings, "Browser data was truncated by the server result limits")
	}
	return warnings
}

// JSON sanitizes one JSON document and returns a bounded replacement.
func JSON(payload []byte, limits Limits) (json.RawMessage, Report, error) {
	if err := limits.Validate(); err != nil {
		return nil, Report{}, err
	}
	if len(payload) > limits.MaxInputBytes {
		return nil, Report{}, ErrInputTooLarge
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, Report{}, fmt.Errorf("decode redaction JSON: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, Report{}, errors.New("decode redaction JSON: multiple values")
		}
		return nil, Report{}, fmt.Errorf("decode redaction JSON trailer: %w", err)
	}

	sanitized, report, err := Value(value, limits)
	if err != nil {
		return nil, Report{}, err
	}
	result, err := json.Marshal(sanitized)
	if err != nil {
		return nil, Report{}, fmt.Errorf("encode redacted JSON: %w", err)
	}
	if len(result) > limits.MaxOutputBytes {
		return nil, report, ErrOutputTooLarge
	}
	return result, report, nil
}

// Value sanitizes an already-decoded JSON-compatible value.
func Value(value any, limits Limits) (any, Report, error) {
	if err := limits.Validate(); err != nil {
		return nil, Report{}, err
	}
	walker := valueWalker{
		limits: limits,
		rules:  make(map[string]struct{}),
	}
	result := walker.walk(value, 0, "")
	rules := make([]string, 0, len(walker.rules))
	for rule := range walker.rules {
		rules = append(rules, rule)
	}
	sort.Strings(rules)
	report := Report{
		Applied:   len(rules) > 0,
		Truncated: walker.truncated,
		Rules:     rules,
	}
	return result, report, nil
}

// String sanitizes secrets and local paths embedded in diagnostic text.
func String(value string, maxBytes int) (string, Report) {
	walker := valueWalker{
		limits: Limits{MaxStringBytes: maxBytes},
		rules:  make(map[string]struct{}),
	}
	result := walker.sanitizeString(value)
	rules := make([]string, 0, len(walker.rules))
	for rule := range walker.rules {
		rules = append(rules, rule)
	}
	sort.Strings(rules)
	return result, Report{
		Applied:   len(rules) > 0,
		Truncated: walker.truncated,
		Rules:     rules,
	}
}

type valueWalker struct {
	limits    Limits
	nodes     int
	truncated bool
	rules     map[string]struct{}
}

func (w *valueWalker) walk(value any, depth int, contextRule string) any {
	if depth > w.limits.MaxDepth || w.nodes >= w.limits.MaxNodes {
		w.truncated = true
		return truncatedValue
	}
	w.nodes++

	switch typed := value.(type) {
	case map[string]any:
		return w.walkObject(typed, depth, contextRule)
	case []any:
		result := make([]any, 0, len(typed))
		for _, item := range typed {
			if w.nodes >= w.limits.MaxNodes {
				w.truncated = true
				result = append(result, truncatedValue)
				break
			}
			result = append(result, w.walk(item, depth+1, contextRule))
		}
		return result
	case string:
		if contextRule != "" {
			w.mark(contextRule)
			return redactedValue
		}
		return w.sanitizeString(typed)
	case nil, bool, json.Number, float64:
		return typed
	default:
		text := fmt.Sprint(typed)
		if contextRule != "" {
			w.mark(contextRule)
			return redactedValue
		}
		return w.sanitizeString(text)
	}
}

func (w *valueWalker) walkObject(value map[string]any, depth int, contextRule string) map[string]any {
	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	identityRule := objectIdentityRule(value)
	result := make(map[string]any, len(value))
	for _, key := range keys {
		if w.nodes >= w.limits.MaxNodes {
			w.truncated = true
			result["truncated"] = truncatedValue
			break
		}
		rule := keyRule(key)
		if rule == "" && isValueKey(key) {
			if identityRule != "" {
				rule = identityRule
			} else if contextRule != "" {
				rule = contextRule
			}
		}
		if rule != "" {
			w.nodes++
			w.mark(rule)
			result[key] = redactedValue
			continue
		}
		if normalizeKey(key) == "attributes" {
			if attributes, ok := w.redactFlatDOMAttributes(value[key]); ok {
				result[key] = w.walk(attributes, depth+1, "")
				continue
			}
		}
		nextContext := contextRuleForKey(key)
		result[key] = w.walk(value[key], depth+1, nextContext)
	}
	return result
}

func (w *valueWalker) redactFlatDOMAttributes(value any) ([]any, bool) {
	attributes, ok := value.([]any)
	if !ok || len(attributes)%2 != 0 {
		return nil, false
	}
	result := append([]any(nil), attributes...)
	passwordInput := false
	for index := 0; index < len(result); index += 2 {
		name, nameOK := result[index].(string)
		attributeValue, valueOK := result[index+1].(string)
		if !nameOK || !valueOK {
			return nil, false
		}
		if normalizeKey(name) == "type" && strings.EqualFold(attributeValue, "password") {
			passwordInput = true
		}
	}
	for index := 0; index < len(result); index += 2 {
		name := result[index].(string)
		rule := keyRule(name)
		if rule == "" && sensitiveIdentityPattern.MatchString(name) {
			rule = "password-fields"
		}
		if rule == "" && passwordInput && normalizeKey(name) == "value" {
			rule = "password-fields"
		}
		if rule != "" {
			result[index+1] = redactedValue
			w.mark(rule)
		}
	}
	return result, true
}

func (w *valueWalker) sanitizeString(value string) string {
	value = replaceAndMark(w, value, urlUserInfoPattern, "${1}[REDACTED]@", "authorization")
	value = replaceAndMark(w, value, bearerPattern, "Bearer [REDACTED]", "authorization")
	value = replaceAndMark(w, value, authorizationPattern, "${1}[REDACTED]", "authorization")
	value = replaceAndMark(w, value, cookieHeaderPattern, "${1}[REDACTED]", "cookies")
	value = replaceAndMark(w, value, querySecretPattern, "${1}[REDACTED]", "query-tokens")
	value = replaceAndMark(w, value, keyValueSecretPattern, "${1}[REDACTED]", "query-tokens")
	value = replaceAndMark(w, value, localPathPattern, redactedPath, "local-paths")
	if w.limits.MaxStringBytes > 0 && len(value) > w.limits.MaxStringBytes {
		value = truncateUTF8(value, w.limits.MaxStringBytes)
		w.truncated = true
	}
	return value
}

func (w *valueWalker) mark(rule string) {
	if rule != "" {
		w.rules[rule] = struct{}{}
	}
}

func replaceAndMark(
	walker *valueWalker,
	value string,
	pattern *regexp.Regexp,
	replacement, rule string,
) string {
	if !pattern.MatchString(value) {
		return value
	}
	walker.mark(rule)
	return pattern.ReplaceAllString(value, replacement)
}

func keyRule(key string) string {
	normalized := normalizeKey(key)
	switch normalized {
	case "authorization", "proxyauthorization":
		return "authorization"
	case "cookie", "cookies", "setcookie", "cookievalue":
		return "cookies"
	case "password", "passwd", "passphrase", "secret", "token", "accesstoken",
		"refreshtoken", "idtoken", "credential", "credentials", "apikey", "xapikey":
		return "password-fields"
	case "formdata", "formvalues", "postdata", "requestbody", "multipart":
		return "form-data"
	case "clipboard", "clipboardtext", "clipboarddata":
		return "clipboard"
	case "filepath", "localpath", "downloadpath", "directorypath", "saveaspath":
		return "local-paths"
	default:
		return ""
	}
}

func contextRuleForKey(key string) string {
	normalized := normalizeKey(key)
	switch normalized {
	case "headers", "requestheaders", "responseheaders":
		return ""
	case "cookiejar":
		return "cookies"
	case "fields", "form", "formfields":
		return "form-data"
	default:
		return ""
	}
}

func objectIdentityRule(value map[string]any) string {
	for _, key := range []string{"name", "id", "type", "autocomplete", "label", "placeholder", "aria-label"} {
		candidate, ok := value[key].(string)
		if !ok || !sensitiveIdentityPattern.MatchString(candidate) {
			continue
		}
		normalized := normalizeKey(candidate)
		switch {
		case strings.Contains(normalized, "authorization"):
			return "authorization"
		case strings.Contains(normalized, "cookie"):
			return "cookies"
		default:
			return "password-fields"
		}
	}
	return ""
}

func isValueKey(key string) bool {
	switch normalizeKey(key) {
	case "value", "values", "text", "content", "data":
		return true
	default:
		return false
	}
}

func normalizeKey(value string) string {
	value = strings.ToLower(value)
	return strings.Map(func(character rune) rune {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' {
			return character
		}
		return -1
	}, value)
}

func truncateUTF8(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	end := maxBytes
	for end > 0 && !utf8.ValidString(value[:end]) {
		end--
	}
	return value[:end]
}
