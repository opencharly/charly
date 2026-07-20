package main

import (
	"testing"

	"github.com/opencharly/sdk/spec"
)

// TestLabelConstantsAreSingular pins every renamed label const to its singular
// wire string. A regression to plural fails the suite — the contract guard.
// (TestExtractMetadata_SingularLabels — the read-path half of this contract —
// moved to sdk/deploykit/labels_singular_test.go with ExtractMetadata, K3-rem.)
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
