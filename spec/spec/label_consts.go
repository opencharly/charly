// Package spec — the OCI label wire-key constants (the ai.opencharly.* contract).
//
// These are the SINGLE SOURCE for the OCI label keys that cross the build↔deploy
// boundary: the build RENDER (sdk/deploykit WriteLabels) EMITS them and the deploy
// readers (ExtractMetadata + the deploy-side consumers) READ them. They are a wire
// contract, not a CUE-sourced type, so they live as hand-written string consts in the
// contract module (the same home as charly_names.go / scalar_aliases.go) — both the
// build emitter and the deploy reader import spec, so ONE copy serves both without a
// charly re-export. The #BoxMetadata / #BakedLabelSet sub-shapes' json tags reproduce
// these keys byte-for-byte (R8 anchor). Relocated from charly/labels.go (#67, the
// build_resolve RENDER-leg death: the deploykit WriteLabels formatter needs the keys
// and cannot import charly).

package spec

const (
	LabelVersion          = "ai.opencharly.version"
	LabelBox              = "ai.opencharly.box"
	LabelRegistry         = "ai.opencharly.registry"
	LabelBootc            = "ai.opencharly.bootc"
	LabelUID              = "ai.opencharly.uid"
	LabelGID              = "ai.opencharly.gid"
	LabelUser             = "ai.opencharly.user"
	LabelHome             = "ai.opencharly.home"
	LabelPort             = "ai.opencharly.port"
	LabelVolume           = "ai.opencharly.volume"
	LabelAlias            = "ai.opencharly.alias"
	LabelSecurity         = "ai.opencharly.security"
	LabelNetwork          = "ai.opencharly.network"
	LabelEnv              = "ai.opencharly.env"
	LabelHook             = "ai.opencharly.hook"
	LabelRoute            = "ai.opencharly.route"
	LabelInit             = "ai.opencharly.init"
	LabelInitDef          = "ai.opencharly.init_def"
	LabelEnvCandy         = "ai.opencharly.env_candy"
	LabelPathAppend       = "ai.opencharly.path_append"
	LabelPortProto        = "ai.opencharly.port_proto"
	LabelPortRelay        = "ai.opencharly.port_relay"
	LabelSkill            = "ai.opencharly.skill"
	LabelStatus           = "ai.opencharly.status"
	LabelInfo             = "ai.opencharly.info"
	LabelCandyVersion     = "ai.opencharly.candy_version"
	LabelSecret           = "ai.opencharly.secret"
	LabelPlatformDistro   = "ai.opencharly.platform.distro"
	LabelPlatformFormat   = "ai.opencharly.platform.format"
	LabelBuilderUse       = "ai.opencharly.builder.use"
	LabelBuilderProvide   = "ai.opencharly.builder.provide"
	LabelDataEntries      = "ai.opencharly.data"
	LabelDataBox          = "ai.opencharly.data_box"
	LabelEnvProvide       = "ai.opencharly.env_provide"
	LabelEnvRequire       = "ai.opencharly.env_require"
	LabelEnvAccept        = "ai.opencharly.env_accept"
	LabelSecretAccept     = "ai.opencharly.secret_accept"  // credential-store-backed env vars this image can optionally use
	LabelSecretRequire    = "ai.opencharly.secret_require" // credential-store-backed env vars this image must have
	LabelMCPProvide       = "ai.opencharly.mcp_provide"
	LabelAgentProvide     = "ai.opencharly.agent_provide"
	LabelTerminalProfiles = "ai.opencharly.terminal_profiles"
	LabelMCPRequire       = "ai.opencharly.mcp_require"
	LabelMCPAccept        = "ai.opencharly.mcp_accept"
	LabelDescription      = "ai.opencharly.description"
	LabelService          = "ai.opencharly.service"
	LabelShell            = "ai.opencharly.shell"
	LabelCheckLevel       = "ai.opencharly.check_level"
)
