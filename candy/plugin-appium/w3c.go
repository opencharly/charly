package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/tebeka/selenium"
)

// w3c.go is the raw-HTTP W3C WebDriver client, moved VERBATIM from charly/appium.go in
// the appium → external-plugin dep-shed. The selenium SDK doesn't expose a "construct
// against an existing session id" constructor (NewRemote always POSTs to create), so for
// every operation AFTER session-create we use the W3C wire protocol directly. W3C is
// stable HTTP/JSON. The ONLY selenium SDK use is session-create's NewRemote (which wraps
// Capabilities under W3C alwaysMatch) — see dispatch.go.

// w3cSession is a raw-HTTP client bound to an existing WebDriver session.
type w3cSession struct {
	BaseURL   string
	SessionID string
	HTTP      *http.Client
}

func newW3CSession(base, sessionID string) *w3cSession {
	return &w3cSession{
		BaseURL:   strings.TrimRight(base, "/"),
		SessionID: sessionID,
		HTTP:      &http.Client{Timeout: 60 * time.Second},
	}
}

// resolveW3CSession reads the session file unless an explicit session override was
// passed, and returns a w3cSession ready for operations.
func resolveW3CSession(box, instance, override string) (*w3cSession, error) {
	sess, err := loadActiveSession(box, instance)
	if err != nil {
		return nil, err
	}
	sid := sess.SessionID
	if override != "" {
		sid = override
	}
	return newW3CSession(sess.BaseURL, sid), nil
}

// call issues a JSON-bodied request to /session/<id>/<endpoint> and returns the W3C
// "value" field of the response. body=nil means GET; any other body means POST/DELETE
// based on method.
func (s *w3cSession) call(method, endpoint string, body any) (json.RawMessage, error) {
	u := s.BaseURL + "/session/" + url.PathEscape(s.SessionID) + endpoint
	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal body: %w", err)
		}
		reqBody = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, u, reqBody)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, u, err)
	}
	defer resp.Body.Close()
	respBytes, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("%s %s: HTTP %d: %s", method, u, resp.StatusCode, strings.TrimSpace(string(respBytes)))
	}
	var envelope struct {
		Value json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(respBytes, &envelope); err != nil {
		return nil, fmt.Errorf("decode %s %s response: %w", method, u, err)
	}
	return envelope.Value, nil
}

// findElement returns the W3C element id for the first match. The element id is wrapped
// under a stable known key in the W3C spec.
const w3cElementKey = "element-6066-11e4-a52e-4f735466cecf"

func (s *w3cSession) findElement(strategy, selector string) (string, error) {
	by, err := strategyToBy(strategy)
	if err != nil {
		return "", err
	}
	body := map[string]string{"using": by, "value": selector}
	resp, err := s.call(http.MethodPost, "/element", body)
	if err != nil {
		return "", fmt.Errorf("find %s=%q: %w", strategy, selector, err)
	}
	var elemMap map[string]string
	if err := json.Unmarshal(resp, &elemMap); err != nil {
		return "", fmt.Errorf("decode element id: %w", err)
	}
	id, ok := elemMap[w3cElementKey]
	if !ok {
		return "", fmt.Errorf("response missing %s key: %s", w3cElementKey, string(resp))
	}
	return id, nil
}

func (s *w3cSession) click(elemID string) error {
	_, err := s.call(http.MethodPost, "/element/"+url.PathEscape(elemID)+"/click", map[string]string{})
	return err
}

func (s *w3cSession) sendKeys(elemID, text string) error {
	_, err := s.call(http.MethodPost, "/element/"+url.PathEscape(elemID)+"/value", map[string]any{"text": text})
	return err
}

func (s *w3cSession) screenshot() ([]byte, error) {
	resp, err := s.call(http.MethodGet, "/screenshot", nil)
	if err != nil {
		return nil, err
	}
	var b64 string
	if err := json.Unmarshal(resp, &b64); err != nil {
		return nil, fmt.Errorf("decode screenshot value: %w", err)
	}
	return base64.StdEncoding.DecodeString(b64)
}

func (s *w3cSession) executeScript(script string, args []any) (json.RawMessage, error) {
	body := map[string]any{"script": script, "args": args}
	return s.call(http.MethodPost, "/execute/sync", body)
}

func (s *w3cSession) elementText(elemID string) (string, error) {
	resp, err := s.call(http.MethodGet, "/element/"+url.PathEscape(elemID)+"/text", nil)
	if err != nil {
		return "", err
	}
	var text string
	if err := json.Unmarshal(resp, &text); err != nil {
		return "", fmt.Errorf("decode element text: %w", err)
	}
	return text, nil
}

func (s *w3cSession) elementAttribute(elemID, name string) (string, error) {
	resp, err := s.call(http.MethodGet, "/element/"+url.PathEscape(elemID)+"/attribute/"+url.PathEscape(name), nil)
	if err != nil {
		return "", err
	}
	// Attributes can come back as a JSON string ("true") or null.
	var v string
	if err := json.Unmarshal(resp, &v); err == nil {
		return v, nil
	}
	return strings.TrimSpace(string(resp)), nil
}

func (s *w3cSession) clearElement(elemID string) error {
	_, err := s.call(http.MethodPost, "/element/"+url.PathEscape(elemID)+"/clear", map[string]any{})
	return err
}

func (s *w3cSession) findElements(strategy, selector string) ([]string, error) {
	by, err := strategyToBy(strategy)
	if err != nil {
		return nil, err
	}
	resp, err := s.call(http.MethodPost, "/elements", map[string]string{"using": by, "value": selector})
	if err != nil {
		return nil, fmt.Errorf("find-all %s=%q: %w", strategy, selector, err)
	}
	var elems []map[string]string
	if err := json.Unmarshal(resp, &elems); err != nil {
		return nil, fmt.Errorf("decode elements: %w", err)
	}
	ids := make([]string, 0, len(elems))
	for _, e := range elems {
		if id, ok := e[w3cElementKey]; ok {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

func (s *w3cSession) source() (string, error) {
	resp, err := s.call(http.MethodGet, "/source", nil)
	if err != nil {
		return "", err
	}
	var src string
	if err := json.Unmarshal(resp, &src); err != nil {
		return "", fmt.Errorf("decode source: %w", err)
	}
	return src, nil
}

// navigateBack uses the session-scoped W3C /back endpoint (call() injects /session/<id>).
func (s *w3cSession) navigateBack() error {
	_, err := s.call(http.MethodPost, "/back", map[string]any{})
	return err
}

func (s *w3cSession) contexts() ([]string, error) {
	resp, err := s.call(http.MethodGet, "/contexts", nil)
	if err != nil {
		return nil, err
	}
	var ctxs []string
	if err := json.Unmarshal(resp, &ctxs); err != nil {
		return nil, fmt.Errorf("decode contexts: %w", err)
	}
	return ctxs, nil
}

func (s *w3cSession) currentContext() (string, error) {
	resp, err := s.call(http.MethodGet, "/context", nil)
	if err != nil {
		return "", err
	}
	var name string
	_ = json.Unmarshal(resp, &name)
	return name, nil
}

func (s *w3cSession) setContext(name string) error {
	_, err := s.call(http.MethodPost, "/context", map[string]any{"name": name})
	return err
}

func (s *w3cSession) orientation() (string, error) {
	resp, err := s.call(http.MethodGet, "/orientation", nil)
	if err != nil {
		return "", err
	}
	var o string
	_ = json.Unmarshal(resp, &o)
	return o, nil
}

func (s *w3cSession) setOrientation(o string) error {
	_, err := s.call(http.MethodPost, "/orientation", map[string]any{"orientation": o})
	return err
}

// rawCall issues an arbitrary W3C call relative to /session/<id>. Backs `appium raw`.
func (s *w3cSession) rawCall(method, path string, body any) (json.RawMessage, error) {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return s.call(method, path, body)
}

// appiumDeleteSessionRemote issues the bare DELETE /session/<id> via plain HTTP.
func appiumDeleteSessionRemote(base, sessionID string) error {
	req, err := http.NewRequest(http.MethodDelete, base+"/session/"+sessionID, nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 && resp.StatusCode != 404 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

// formatW3CValue renders a W3C value cleanly: JSON strings are unquoted, objects/arrays
// print as compact JSON, null/empty returns the fallback token. The string-returning
// form of charly/appium.go's printW3CValue (the plugin captures output instead of
// printing it, so the host can run the stdout matchers).
func formatW3CValue(result json.RawMessage, fallback string) string {
	trimmed := strings.TrimSpace(string(result))
	if trimmed == "" || trimmed == "null" {
		return fallback
	}
	var str string
	if json.Unmarshal(result, &str) == nil {
		return str
	}
	return trimmed
}

// substituteElement replaces the literal {element} token with a resolved id.
func substituteElement(s, elemID string) string {
	return strings.ReplaceAll(s, "{element}", elemID)
}

// strategyToBy maps the charly authoring surface to the SDK / W3C constants.
func strategyToBy(strategy string) (string, error) {
	switch strings.ToLower(strategy) {
	case "", "xpath":
		return selenium.ByXPATH, nil
	case "id":
		return selenium.ByID, nil
	case "accessibility-id":
		return "accessibility id", nil
	case "class-name":
		return selenium.ByClassName, nil
	case "android-uiautomator":
		return "-android uiautomator", nil
	case "name":
		return selenium.ByName, nil
	case "css":
		return selenium.ByCSSSelector, nil
	}
	return "", fmt.Errorf("unknown strategy %q (allowed: xpath, id, accessibility-id, class-name, android-uiautomator, name, css)", strategy)
}
