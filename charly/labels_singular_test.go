package main

import (
	"encoding/json"
	"testing"

	"github.com/opencharly/sdk/spec"
)

// TestExtractMetadata_SingularLabels proves the 2026-06 singular-label
// contract end-to-end on the read path: ExtractMetadata reads the SINGULAR
// `ai.opencharly.*` keys into the renamed BoxMetadata fields. The keys are
// written as LITERAL strings (not via the consts), so if any label const ever
// regresses to plural, ExtractMetadata — which reads via the const — won't find
// the literal key and the field stays empty, failing this test.
func TestExtractMetadata_SingularLabels(t *testing.T) {
	orig := InspectLabels
	defer func() { InspectLabels = orig }()

	svcBlob, _ := json.Marshal([]CapabilityService{{Name: "web", Init: "supervisord"}})
	envBlob, _ := json.Marshal(map[string]string{"OLLAMA_HOST": "http://{{.ContainerName}}:11434"})

	InspectLabels = func(engine, imageRef string) (map[string]string, error) {
		return map[string]string{
			"ai.opencharly.version":     "2026.155.1801",
			"ai.opencharly.image":       "demo",
			"ai.opencharly.port":        `["8080:8080"]`,
			"ai.opencharly.service":     string(svcBlob),
			"ai.opencharly.env_provide": string(envBlob),
		}, nil
	}

	meta, err := ExtractMetadata("podman", "ghcr.io/opencharly/demo:test")
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(meta.Port) != 1 || meta.Port[0] != "8080:8080" {
		t.Errorf("singular ai.opencharly.port → meta.Port: got %+v", meta.Port)
	}
	if len(meta.Service) != 1 || meta.Service[0].Name != "web" {
		t.Errorf("singular ai.opencharly.service → meta.Service: got %+v", meta.Service)
	}
	if meta.EnvProvide["OLLAMA_HOST"] == "" {
		t.Errorf("singular ai.opencharly.env_provide → meta.EnvProvide: got %+v", meta.EnvProvide)
	}
}

// TestLabelConstantsAreSingular pins every renamed label const to its singular
// wire string. A regression to plural fails the suite — the contract guard.
func TestLabelConstantsAreSingular(t *testing.T) {
	pairs := []struct{ got, want string }{
		{spec.LabelPort, "ai.opencharly.port"},
		{spec.LabelVolume, "ai.opencharly.volume"},
		{spec.LabelAlias, "ai.opencharly.alias"},
		{spec.LabelHook, "ai.opencharly.hook"},
		{spec.LabelRoute, "ai.opencharly.route"},
		{spec.LabelSecret, "ai.opencharly.secret"},
		{spec.LabelService, "ai.opencharly.service"},
		{spec.LabelSkill, "ai.opencharly.skill"},
		{spec.LabelEnvCandy, "ai.opencharly.env_candy"},
		{spec.LabelPortProto, "ai.opencharly.port_proto"},
		{spec.LabelCandyVersion, "ai.opencharly.candy_version"},
		{spec.LabelPlatformFormat, "ai.opencharly.platform.format"},
		{spec.LabelBuilderUse, "ai.opencharly.builder.use"},
		{spec.LabelBuilderProvide, "ai.opencharly.builder.provide"},
		{spec.LabelEnvProvide, "ai.opencharly.env_provide"},
		{spec.LabelEnvRequire, "ai.opencharly.env_require"},
		{spec.LabelEnvAccept, "ai.opencharly.env_accept"},
		{spec.LabelSecretAccept, "ai.opencharly.secret_accept"},
		{spec.LabelSecretRequire, "ai.opencharly.secret_require"},
		{spec.LabelMCPProvide, "ai.opencharly.mcp_provide"},
		{spec.LabelAgentProvide, "ai.opencharly.agent_provide"},
		{spec.LabelTerminalProfiles, "ai.opencharly.terminal_profiles"},
		{spec.LabelMCPRequire, "ai.opencharly.mcp_require"},
		{spec.LabelMCPAccept, "ai.opencharly.mcp_accept"},
	}
	for _, p := range pairs {
		if p.got != p.want {
			t.Errorf("label const = %q, want singular %q", p.got, p.want)
		}
	}
}
