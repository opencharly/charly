package spec

// http_do.go — the host-side HTTP-do path for the `http` check verb, RELOCATED to the spec
// contract module (#55 CHECK-ENGINE cone Option A — the check-verb host-vantage HTTP family:
// net/http + crypto/tls host primitives operate only on the spec.CheckHTTPRequest /
// spec.CheckHTTPResponse wire types, so charly core's check dispatch reaches them importing
// zero kit). The ONE host-side HTTP-do path shared by the in-proc check context
// (hostCheckContext.HTTPDo) AND the out-of-process CheckContextService.HTTPDo RPC leg (R3,
// single source). sdk/kit re-exports each symbol (sdk/kit/http_do.go) so every existing
// kit.DoHTTPRequest / kit.HTTPClientFor / kit.FormatHTTPHeaders call site (the candies + sdk)
// is untouched. New consumers reference spec.* directly.

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
)

// HTTPClientFor builds a per-request *http.Client honoring the CheckHTTPRequest policy
// (AllowInsecure, NoFollowRedirects, CAPEM, Timeout), derived from the engine's base
// client. The base supplies the default timeout; req.Timeout overrides it.
func HTTPClientFor(base *http.Client, req CheckHTTPRequest) (*http.Client, error) {
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

// DoHTTPRequest issues req from the HOST's network namespace using a client built from base
// + req's per-request policy, returning the status, the body, and the formatted
// response-header blob. The ONE host-side HTTP-do path shared by the in-proc check context
// AND the CheckContextService reverse channel (R3). A transport-level failure is returned as
// err; a non-2xx is NOT an error (the caller matches resp.Status).
func DoHTTPRequest(ctx context.Context, base *http.Client, req CheckHTTPRequest) (CheckHTTPResponse, error) {
	client, err := HTTPClientFor(base, req)
	if err != nil {
		return CheckHTTPResponse{}, err
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
		return CheckHTTPResponse{}, err
	}
	for k, v := range req.Headers {
		hreq.Header.Set(k, v)
	}
	resp, err := client.Do(hreq)
	if err != nil {
		return CheckHTTPResponse{}, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return CheckHTTPResponse{}, err
	}
	return CheckHTTPResponse{Status: resp.StatusCode, Body: respBody, HeaderBlob: FormatHTTPHeaders(resp.Header)}, nil
}

// FormatHTTPHeaders renders an http.Header into a "Key: value\n" blob (one line per value,
// multi-value preserved) — the matcher-ready response-header form.
func FormatHTTPHeaders(h http.Header) string {
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
