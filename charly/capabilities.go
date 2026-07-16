package main

import (
	"fmt"
	"reflect"

	"github.com/opencharly/sdk/spec"
)

// -----------------------------------------------------------------------------
// Capabilities — the image's runtime contract. Part G of the refactor plan.
//
// Every field listed here MUST have an OCI label home (labelFields table below).
// This is the "what can this image do, what does it need, what does it provide"
// view baked into OCI labels at build time and read back at deploy time, with
// no dependence on the source repo's charly.yml. The self-deploy invariant
// (Part F.10: `charly bundle from-box`) depends on this list being complete.
//
// Storage note: today the on-disk representation of capabilities is the existing
// BoxMetadata struct (charly/labels.go). Capabilities is an alias that fixes the
// naming + provides a label-completeness check + a typed helper for loading
// from a pushed OCI image by ref alone. A future schema-level split of
// BoxConfig into image.build: + image.capabilities: (which charly migrate
// unified would emit) reuses this same type.
// -----------------------------------------------------------------------------

// Capabilities names the same data as BoxMetadata — it is the runtime
// contract loaded from OCI labels. Using a type alias keeps every existing
// BoxMetadata consumer unchanged while letting new code (Part F K8s
// generator, charly bundle from-box) use the canonical name.
type Capabilities = BoxMetadata

// CapabilityLabelMap names every OCI label that participates in the
// capabilities contract. Maintained alongside BoxMetadata — adding a field
// to BoxMetadata without adding an entry here trips the completeness check
// below and breaks the build.
var CapabilityLabelMap = map[string]string{
	// Identity
	"Box":          spec.LabelBox,
	"Version":      spec.LabelVersion,
	"Registry":     spec.LabelRegistry,
	"Bootc":        spec.LabelBootc,
	"Status":       spec.LabelStatus,
	"Info":         spec.LabelInfo,
	"CandyVersion": spec.LabelCandyVersion,

	// Account
	"UID":  spec.LabelUID,
	"GID":  spec.LabelGID,
	"User": spec.LabelUser,
	"Home": spec.LabelHome,

	// Ports / volumes / aliases / routes
	"Port":      spec.LabelPort,
	"PortProto": spec.LabelPortProto,
	"PortRelay": spec.LabelPortRelay,
	"Volume":    spec.LabelVolume,
	"Alias":     spec.LabelAlias,
	"Route":     spec.LabelRoute,

	// Security
	"Security": spec.LabelSecurity,

	// Networking — image-declared network mode. Tunnel / DNS / AcmeEmail
	// moved to BundleNode in schema v4 (deployment choices, no
	// image-declaration meaning).
	"Network": spec.LabelNetwork,

	// Env / vars
	"Env":        spec.LabelEnv,
	"EnvCandy":   spec.LabelEnvCandy,
	"PathAppend": spec.LabelPathAppend,

	// Init — auto-detected from candies (see init_config.go ResolveInitSystem).
	// Engine moved to BundleNode in schema v4 (deploy-host choice).
	"Init":         spec.LabelInit,
	"InitDef":      spec.LabelInitDef,
	"Service":      spec.LabelService,
	"ServiceNames": spec.LabelInit,

	// Distro + build formats + builder provides
	"Distro":      spec.LabelPlatformDistro,
	"BuildFormat": spec.LabelPlatformFormat,
	"Builder":     spec.LabelBuilderUse,
	"Build":       spec.LabelBuilderProvide,

	// Hooks
	"Hook": spec.LabelHook,

	// Skills (doc pointer)
	"Skill": spec.LabelSkill,

	// Data seeding
	"DataEntries": spec.LabelDataEntries,
	"DataImage":   spec.LabelDataBox,

	// Env / secret / MCP dependency graph
	"EnvProvide":    spec.LabelEnvProvide,
	"EnvRequire":    spec.LabelEnvRequire,
	"EnvAccept":     spec.LabelEnvAccept,
	"SecretAccept":  spec.LabelSecretAccept,
	"SecretRequire": spec.LabelSecretRequire,
	"Secret":        spec.LabelSecret,
	"MCPProvide":    spec.LabelMCPProvide,
	"MCPRequire":    spec.LabelMCPRequire,
	"MCPAccept":     spec.LabelMCPAccept,

	// plan-shaped self-description — three-section (candy/box/deploy)
	// LabelDescriptionSet. The description label set is additive; the
	// Info/Status fields remain on BoxMetadata alongside it.
	"Description": spec.LabelDescription,

	// Shell-init manifest — three-section (candy/box/deploy) per-shell
	// rc-snippet contributions. 2026-05 cutover. Read by `charly box
	// inspect`, `charly bundle from-box`, and the charly.yml `shell:`
	// overlay merge in MergeDeployShell.
	"Shell": spec.LabelShell,

	// Acceptance-depth rung (none|build|noagent|agent) gating how deep
	// `charly check run <bed>` drives this box. See check_level.go.
	"CheckLevel": spec.LabelCheckLevel,
}

// deployOnlyCapabilityFields are BoxMetadata fields that are NOT baked
// as OCI labels by design — they're populated from charly.yml overlays
// (or deploy-host config) and have no image-declaration meaning. The
// completeness check exempts them from CapabilityLabelMap mapping.
//
// This list codifies the schema v4 migration note on labels.go:33-36:
// "Tunnel / DNS / AcmeEmail / Engine moved to BundleNode". The fields
// stay on BoxMetadata because deploy-mode commands still consume them
// after MergeDeployOntoMetadata runs — but they never round-trip through
// OCI labels.
var deployOnlyCapabilityFields = map[string]bool{
	"Tunnel":    true,
	"DNS":       true,
	"AcmeEmail": true,
	"Engine":    true,
}

// checkCapabilityLabelCompleteness returns an error listing any BoxMetadata
// exported field that lacks an entry in CapabilityLabelMap. Called from
// TestCapabilityLabelCompleteness to fail the build when a field is added
// without a label mapping.
func checkCapabilityLabelCompleteness() error {
	rt := reflect.TypeFor[BoxMetadata]()
	var missing []string
	for field := range rt.Fields() {
		name := field.Name
		if deployOnlyCapabilityFields[name] {
			continue
		}
		if _, ok := CapabilityLabelMap[name]; !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("BoxMetadata fields without CapabilityLabelMap entry: %v", missing)
	}
	return nil
}

// CapabilitiesFromLabels is the source-less loader used by `charly bundle
// from-box` (Part F.10): given only an engine + image ref, pull OCI labels
// via inspect and produce a Capabilities struct. No charly.yml, no source
// repo access required. Errors propagate ErrImageNotLocal when appropriate
// (caller can wrap with a "run charly box pull" hint).
func CapabilitiesFromLabels(engine, imageRef string) (*Capabilities, error) {
	meta, err := ExtractMetadata(engine, imageRef)
	if err != nil {
		return nil, err
	}
	if meta == nil {
		return nil, fmt.Errorf("image %q has no ai.opencharly labels (not an opencharly image?)", imageRef)
	}
	return meta, nil
}
