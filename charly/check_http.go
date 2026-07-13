package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/opencharly/sdk/kit"
)

// httpClientFor builds a per-request *http.Client honoring the kit.HTTPRequest policy
// (AllowInsecure, NoFollowRedirects, CAPEM, Timeout), derived from the engine's base
// client. HOST-side: the SINGLE client builder for BOTH the in-proc check context
// (hostCheckContext.HTTPDo) AND the out-of-process CheckContextService.HTTPDo (R3 —
// relocated from candy/plugin-http/plugin.go, which no longer holds the live client). The
// base supplies the default timeout; req.Timeout overrides it.
func httpClientFor(base *http.Client, req kit.HTTPRequest) (*http.Client, error) {
	client := &http.Client{}
	if base != nil {
		client.Timeout = base.Timeout
	}
	if req.Timeout != "" {
		if d, err := time.ParseDuration(req.Timeout); err == nil {
			client.Timeout = d
		}
	}
	tr := &http.Transport{}
	if req.AllowInsecure {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	if len(req.CAPEM) > 0 {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(req.CAPEM) {
			return nil, fmt.Errorf("no certs parsed from CA PEM")
		}
		if tr.TLSClientConfig == nil {
			tr.TLSClientConfig = &tls.Config{}
		}
		tr.TLSClientConfig.RootCAs = pool
	}
	client.Transport = tr
	if req.NoFollowRedirects {
		client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	}
	return client, nil
}

// doHTTPRequest issues req from the HOST's network namespace using a client built from base
// + req's per-request policy, returning the status, the body, and the formatted
// response-header blob. The ONE host-side HTTP-do path shared by the in-proc check context
// AND the CheckContextService reverse channel (R3). A transport-level failure is returned as
// err; a non-2xx is NOT an error (the caller matches resp.Status).
func doHTTPRequest(ctx context.Context, base *http.Client, req kit.HTTPRequest) (kit.HTTPResponse, error) {
	client, err := httpClientFor(base, req)
	if err != nil {
		return kit.HTTPResponse{}, err
	}
	method := req.Method
	if method == "" {
		method = "GET"
	}
	var body io.Reader
	if len(req.Body) > 0 {
		body = bytes.NewReader(req.Body)
	}
	hreq, err := http.NewRequestWithContext(ctx, method, req.URL, body)
	if err != nil {
		return kit.HTTPResponse{}, err
	}
	for k, v := range req.Headers {
		hreq.Header.Set(k, v)
	}
	resp, err := client.Do(hreq)
	if err != nil {
		return kit.HTTPResponse{}, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return kit.HTTPResponse{}, err
	}
	return kit.HTTPResponse{Status: resp.StatusCode, Body: respBody, HeaderBlob: formatHTTPHeaders(resp.Header)}, nil
}

// formatHTTPHeaders renders an http.Header into a "Key: value\n" blob (one line per value,
// multi-value preserved) — the matcher-ready response-header form (relocated from
// candy/plugin-http's formatHeaders, R3).
func formatHTTPHeaders(h http.Header) string {
	var b strings.Builder
	for k, vs := range h {
		for _, v := range vs {
			b.WriteString(k)
			b.WriteString(": ")
			b.WriteString(v)
			b.WriteString("\n")
		}
	}
	return b.String()
}
