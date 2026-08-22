package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	protobuf "github.com/emicklei/proto"
)

func TestGeneratedProtocolReproducible(t *testing.T) {
	model, err := loadModel(filepath.Join("..", "..", "protocol", "schema"))
	if err != nil {
		t.Fatal(err)
	}
	if err := validateModel(&model); err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join("..", "..", "proto", "plugin.proto"))
	if err != nil {
		t.Fatal(err)
	}
	if got := renderProto(&model); !bytes.Equal(got, want) {
		t.Fatal("proto/plugin.proto drifted from protocol/schema/*.cue; run task wire:gen")
	}
}

func TestCurrentProtocolRoundTrip(t *testing.T) {
	path := filepath.Join("..", "..", "proto", "plugin.proto")
	original := parseProtoModel(t, path)
	if err := validateModel(&original); err != nil {
		t.Fatalf("validate original: %v", err)
	}
	tmp := filepath.Join(t.TempDir(), "plugin.proto")
	if err := os.WriteFile(tmp, renderProto(&original), 0o644); err != nil {
		t.Fatal(err)
	}
	roundTrip := parseProtoModel(t, tmp)
	if !reflect.DeepEqual(original, roundTrip) {
		t.Fatalf("protobuf model changed on CUE-generator round trip\noriginal: %#v\nroundtrip: %#v", original, roundTrip)
	}
}

func TestValidateModelRejectsDuplicateFieldNumber(t *testing.T) {
	model := protocolModel{
		Syntax: "proto3", Package: "test", GoPackage: "example.test/proto",
		Messages: []messageModel{{Name: "Request", Fields: []fieldModel{
			{Name: "first", Type: "string", Number: 1},
			{Name: "second", Type: "string", Number: 1},
		}}},
	}
	if err := validateModel(&model); err == nil {
		t.Fatal("expected duplicate field number to fail")
	}
}

// TestProtocolDocsComplete locks the FULL doc text of every documented
// element in protocol/schema/*.cue. The bootstrap importer truncated every
// multi-line // comment block to its first line (emicklei/proto's
// Comment.Message() returns only Lines[0]) and shifted trailing one-line
// message comments onto the next element — defects the round-trip test cannot
// see once the truncated text is baked into both the CUE model and the
// generated proto. Each phrase below is continuation text the truncation
// dropped (or attribution the shift lost), so this test fails the moment a
// doc regresses to a first-line stub, lands on the wrong element, or the
// element is renamed without updating the expectation.
func TestProtocolDocsComplete(t *testing.T) {
	model, err := loadModel(filepath.Join("..", "..", "protocol", "schema"))
	if err != nil {
		t.Fatal(err)
	}
	messageDocs := map[string]string{}
	for _, message := range model.Messages {
		messageDocs[message.Name] = message.Doc
	}
	serviceDocs := map[string]string{}
	methodDocs := map[string]string{}
	for _, service := range model.Services {
		serviceDocs[service.Name] = service.Doc
		for _, method := range service.Methods {
			methodDocs[service.Name+"."+method.Name] = method.Doc
		}
	}

	// minLen is the byte floor a doc must carry; the truncated stubs were
	// single ~80-character first lines, so anything below the floor is a
	// re-truncation.
	type docExpectation struct {
		doc     string
		minLen  int
		phrases []string
	}
	expectations := map[string]docExpectation{}
	addMessages := func(minLen int, docs map[string][]string) {
		for name, phrases := range docs {
			expectations["message "+name] = docExpectation{messageDocs[name], minLen, phrases}
		}
	}
	addMessages(120, map[string][]string{
		"ProvidedCapability":           {"plugin_input. The schema travels with the plugin", "no Title-case naming convention"},
		"DeployTraits":                 {"Canonical table: pod=container", "external-in-place"},
		"StepContract":                 {"WITHOUT a compiled-in case", "non-step capabilities"},
		"InvokeProviderRequest":        {"reverse context): dispatch op"},
		"HostBuildRequest":             {"kustomize", "artifact path/handle JSON"},
		"HostStepRequest":              {"record-and-replay teardown", "RunReply/CaptureReply"},
		"HTTPDoRequest":                {"httpClientFor", "Go duration string"},
		"HTTPDoReply":                  {"a failed request rides the error field"},
		"ResolveGraphicsEndpointReply": {"skip_message", "N/A skip, not a failure"},
	})
	addMessages(0, map[string][]string{
		// Attribution locks: these docs belong to THIS element — the
		// bootstrap shift had moved each one to its neighbour.
		"InvokeReply":    {"CheckResult / InstallPlan / Diagnostics, JSON"},
		"Frame":          {"one streamed result frame (single-shot sends one)"},
		"RunRequest":     {"opts_json = EmitOpts, JSON"},
		"RunReply":       {"empty error = success"},
		"PutFileRequest": {"content placed at path; mode = octal perms"},
		"PutFileReply":   {"empty error = success"},
		"CaptureReply":   {"error = execution failure, NOT a non-zero exit"},
		"LiveReply":      {"stdout/stderr/stdin went LIVE to the operator's terminal"},
	})
	services := map[string][]string{
		"Provider":            {"snapshotCheckEnv", "reserved` is the reserved word"},
		"ExecutorService":     {"The plugin owns the plan WALK ordering", "GRPCBroker", "NESTED reverse channel"},
		"CheckContextService": {"Uniform API Invariant", "NOT this service"},
	}
	for name, phrases := range services {
		expectations["service "+name] = docExpectation{serviceDocs[name], 120, phrases}
	}
	methods := map[string][]string{
		"CheckContextService.ResolveEndpoint":         {"ssh -L forward", "Class-"},
		"CheckContextService.ResolveGraphicsEndpoint": {"go-libvirt", "never a per-verb RPC"},
		"CheckContextService.ResolveImageLabel":       {"ai.opencharly.mcp_provide", "label absent"},
	}
	for name, phrases := range methods {
		expectations["method "+name] = docExpectation{methodDocs[name], 120, phrases}
	}

	for name, want := range expectations {
		if want.doc == "" {
			t.Errorf("%s: missing doc (element absent or doc dropped)", name)
			continue
		}
		if len(want.doc) < want.minLen {
			t.Errorf("%s: doc is %d bytes (< %d) — truncated to a first-line stub?: %q", name, len(want.doc), want.minLen, want.doc)
		}
		for _, phrase := range want.phrases {
			if !strings.Contains(want.doc, phrase) {
				t.Errorf("%s: doc missing continuation text %q — truncated or mis-homed?: %q", name, phrase, want.doc)
			}
		}
	}
	// Mis-homed docs the bootstrap shift produced: Frame carried InvokeReply's
	// doc and GetFileRequest carried LiveReply's. Both must stay clean.
	if strings.Contains(messageDocs["Frame"], "CheckResult / InstallPlan") {
		t.Errorf("message Frame: carries InvokeReply's doc — the one-line-message comment shift is back: %q", messageDocs["Frame"])
	}
	if messageDocs["GetFileRequest"] != "" {
		t.Errorf("message GetFileRequest: undocumented in the source proto, must not carry a shifted doc: %q", messageDocs["GetFileRequest"])
	}
}

func parseProtoModel(t *testing.T, path string) protocolModel {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := f.Close(); err != nil {
			t.Errorf("close protocol fixture: %v", err)
		}
	})
	parsed, err := protobuf.NewParser(f).Parse()
	if err != nil {
		t.Fatal(err)
	}
	model, err := modelFromProto(parsed)
	if err != nil {
		t.Fatal(err)
	}
	return model
}
