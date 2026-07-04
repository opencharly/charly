package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/opencharly/sdk/kit"
)

// TestDoHTTPRequest exercises the host-side HTTP execution shared by the in-proc
// runnerCheckContext.HTTPDo and the out-of-process CheckContextService.HTTPDo (F2 — R3):
// status, body + header blob, custom timeout, allow_insecure against a self-signed TLS
// server, and no_follow_redirects. doHTTPRequest issues from the host's network namespace
// applying the per-request policy carried in kit.HTTPRequest — the leg that proves the
// out-of-process http verb dials with HOST-vantage semantics identical to compiled-in.
func TestDoHTTPRequest(t *testing.T) {
	base := &http.Client{Timeout: 10 * time.Second}

	t.Run("status + body + header", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("X-Charly", "yes")
			w.WriteHeader(200)
			_, _ = w.Write([]byte("service is ready"))
		}))
		defer srv.Close()
		resp, err := doHTTPRequest(context.Background(), base, kit.HTTPRequest{URL: srv.URL})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp.Status != 200 {
			t.Errorf("status = %d, want 200", resp.Status)
		}
		if !strings.Contains(string(resp.Body), "ready") {
			t.Errorf("body = %q, want it to contain 'ready'", resp.Body)
		}
		if !strings.Contains(resp.HeaderBlob, "X-Charly: yes") {
			t.Errorf("header blob = %q, want it to contain 'X-Charly: yes'", resp.HeaderBlob)
		}
	})

	t.Run("allow_insecure against self-signed TLS", func(t *testing.T) {
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(204)
		}))
		defer srv.Close()
		// Without AllowInsecure the self-signed cert fails verification.
		if _, err := doHTTPRequest(context.Background(), base, kit.HTTPRequest{URL: srv.URL}); err == nil {
			t.Error("expected a TLS verification error without AllowInsecure, got nil")
		}
		// With AllowInsecure the request succeeds.
		resp, err := doHTTPRequest(context.Background(), base, kit.HTTPRequest{URL: srv.URL, AllowInsecure: true})
		if err != nil {
			t.Fatalf("AllowInsecure: unexpected error: %v", err)
		}
		if resp.Status != 204 {
			t.Errorf("AllowInsecure status = %d, want 204", resp.Status)
		}
	})

	t.Run("custom timeout", func(t *testing.T) {
		slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			time.Sleep(100 * time.Millisecond)
			w.WriteHeader(200)
		}))
		defer slow.Close()
		if _, err := doHTTPRequest(context.Background(), base, kit.HTTPRequest{URL: slow.URL, Timeout: "10ms"}); err == nil {
			t.Error("expected a timeout error with Timeout=10ms, got nil")
		}
	})

	t.Run("no_follow_redirects", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if req.URL.Path == "/redir" {
				http.Redirect(w, req, "/dest", http.StatusFound)
				return
			}
			w.WriteHeader(200)
		}))
		defer srv.Close()
		// Following (default): lands on /dest → 200.
		if resp, err := doHTTPRequest(context.Background(), base, kit.HTTPRequest{URL: srv.URL + "/redir"}); err != nil || resp.Status != 200 {
			t.Errorf("follow: status=%d err=%v, want 200/nil", resp.Status, err)
		}
		// NoFollowRedirects: returns the 302 itself.
		resp, err := doHTTPRequest(context.Background(), base, kit.HTTPRequest{URL: srv.URL + "/redir", NoFollowRedirects: true})
		if err != nil {
			t.Fatalf("no-follow: unexpected error: %v", err)
		}
		if resp.Status != http.StatusFound {
			t.Errorf("no-follow status = %d, want 302", resp.Status)
		}
	})
}
