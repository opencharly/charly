// Package httpverb is the importable host-coupled `http` check verb: an HTTP request
// matched against status / body / headers — issued from the charly host's network
// namespace under charly check live (via cc.HTTPDo — in-process the engine dials, out of
// process the request crosses the CheckContextService reverse channel and the host dials),
// or from inside the disposable container via `curl` under charly check box (cc.Exec). It
// implements kit.CheckVerbProvider and runs in EITHER placement (compiled-in OR
// out-of-process, F2) with ZERO authoring change. Relocated out of charly's module
// (formerly charly/plugin/builtins/http + charly/plugin_http.go) onto the sdk/kit
// contract. The matcher evaluation reuses the importable sdk.MatchAll + spec.MatcherList (R3).
package httpverb

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/opencharly/charly/candy/plugin-http/params"
	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/kit"
	pb "github.com/opencharly/sdk/proto"
	"github.com/opencharly/sdk/spec"
)

//go:embed schema/*.cue
var schemaFS embed.FS

// NewCheckVerb returns the http verb as a kit.CheckVerbProvider for compiled-in registration.
func NewCheckVerb() kit.CheckVerbProvider { return verb{} }

// NewMeta advertises verb:http (plugin_input #HttpInput) + the embedded CUE schema, via
// sdk.NewMeta — the ONE meta both placements use (compiled-in registerCompiledCheckVerb reads
// it via Describe; cmd/serve serves it out-of-process), so a kit candy has the SAME
// NewCheckVerb()+NewMeta() shape as every pb-provider plugin (R3).
func NewMeta() pb.PluginMetaServer {
	return sdk.NewMeta("2026.176.2200",
		[]sdk.ProvidedCapability{{Class: "verb", Word: "http", InputDef: "#HttpInput"}},
		schemaFS)
}

type verb struct{}

func (verb) Reserved() string { return "http" }

// RunVerb decodes the typed plugin_input (params.HttpInput) and runs the request via the
// live CheckContext. gengotypes degrades the body/header matcher disjunction to `any`, so
// each is re-decoded through the shared matcher codec (spec.MatcherList.UnmarshalJSON) into
// the typed list sdk.MatchAll consumes. The SHARED method/request_body modifiers + the
// general timeout stay base #Op fields, read off op directly (mirrors the former r.runHTTP).
func (verb) RunVerb(ctx context.Context, cc kit.CheckContext, op *spec.Op) kit.Result {
	var in params.HttpInput
	kit.DecodeInput(op.PluginInput, &in)
	h := httpCheck{
		URL:           in.HTTP,
		Status:        in.Status,
		Body:          decodeMatcherList(in.Body),
		Headers:       decodeMatcherList(in.Headers),
		AllowInsecure: in.AllowInsecure,
		NoFollowRedir: in.NoFollowRedir,
		CAFile:        in.CAFile,
	}
	if cc.Mode() == kit.ModeBox {
		return runHTTPInContainer(ctx, cc, op, h)
	}
	return runHTTPFromHost(ctx, cc, op, h)
}

// httpCheck carries the http verb's plugin_input-decoded fields. The SHARED
// method/request_body modifiers and the general timeout stay base #Op fields, read off op.
type httpCheck struct {
	URL           string
	Status        int
	Body          spec.MatcherList
	Headers       spec.MatcherList
	AllowInsecure bool
	NoFollowRedir bool
	CAFile        string
}

func runHTTPFromHost(ctx context.Context, cc kit.CheckContext, op *spec.Op, h httpCheck) kit.Result {
	// The host issues the request from its own network namespace via cc.HTTPDo — in-process
	// the engine dials directly, out-of-process the request crosses the CheckContextService
	// reverse channel and the host dials (an *http.Client cannot cross the wire). The candy
	// resolves its authored ca_file to PEM bytes host-side so the request is self-contained.
	req := kit.HTTPRequest{
		Method:            op.Method,
		URL:               h.URL,
		AllowInsecure:     h.AllowInsecure,
		NoFollowRedirects: h.NoFollowRedir,
		Timeout:           op.Timeout,
	}
	if op.RequestBody != "" {
		req.Body = []byte(op.RequestBody)
	}
	if h.CAFile != "" {
		pem, err := os.ReadFile(h.CAFile)
		if err != nil {
			return kit.Failf("reading CA file: %v", err)
		}
		req.CAPEM = pem
	}
	resp, err := cc.HTTPDo(ctx, req)
	if err != nil {
		return kit.Failf("request: %v", err)
	}
	if h.Status != 0 && resp.Status != h.Status {
		return kit.Failf("status=%d, want %d", resp.Status, h.Status)
	}
	if len(h.Headers) > 0 {
		if err := sdk.MatchAll(resp.HeaderBlob, h.Headers); err != nil {
			return kit.Failf("headers: %v", err)
		}
	}
	if len(h.Body) > 0 {
		if err := sdk.MatchAll(string(resp.Body), h.Body); err != nil {
			return kit.Failf("body: %v", err)
		}
	}
	return kit.Passf("status=%d", resp.Status)
}

func runHTTPInContainer(ctx context.Context, cc kit.CheckContext, _ *spec.Op, h httpCheck) kit.Result {
	// In-container HTTP via curl. We only check status/body here; full header-matching
	// is host-side. Sufficient to validate the service inside the disposable container
	// answers.
	cmd := fmt.Sprintf("curl -sS -o /tmp/.charly-test-body -w '%%{http_code}' %s", kit.ShellQuote(h.URL))
	if h.AllowInsecure {
		cmd = "curl -sSk -o /tmp/.charly-test-body -w '%{http_code}' " + kit.ShellQuote(h.URL)
	}
	stdout, stderr, exit, err := cc.Exec().RunCapture(ctx, cmd)
	if err != nil || exit != 0 {
		return kit.Failf("curl exit %d err %v (%s)", exit, err, trimPreview(stderr))
	}
	code, convErr := strconv.Atoi(strings.TrimSpace(stdout))
	if convErr != nil {
		return kit.Failf("unexpected curl output: %q", stdout)
	}
	if h.Status != 0 && code != h.Status {
		return kit.Failf("status=%d, want %d", code, h.Status)
	}
	if len(h.Body) > 0 {
		body, _, exit, err := cc.Exec().RunCapture(ctx, "cat /tmp/.charly-test-body")
		if err != nil || exit != 0 {
			return kit.Failf("reading body: exit=%d err=%v", exit, err)
		}
		if err := sdk.MatchAll(body, h.Body); err != nil {
			return kit.Failf("body: %v", err)
		}
	}
	return kit.Passf("status=%d", code)
}

// decodeMatcherList re-decodes a gengotypes-degraded matcher value (`any`) through the
// shared spec.MatcherList JSON codec. A nil / unparseable value yields a nil list.
func decodeMatcherList(v any) spec.MatcherList {
	if v == nil {
		return nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var ml spec.MatcherList
	if err := json.Unmarshal(raw, &ml); err != nil {
		return nil
	}
	return ml
}

// trimPreview is the shared kit helper (FU-11 — formerly duplicated in core + plugins).
var trimPreview = kit.TrimPreview
