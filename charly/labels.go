package main

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// ErrImageNotLocal (P12a: promoted to sdk/kit/local_image.go, referenced here as
// kit.ErrImageNotLocal) is returned when ExtractMetadata is called on an image
// that is not present in the engine's local storage. Deploy-mode commands
// unwrap this sentinel at the error boundary to render a recommendation
// pointing users to `charly box pull`.

// OCI label key constants (all namespaced under ai.opencharly.) live in sdk/spec
// (label_consts.go — the build↔deploy wire contract: the deploykit WriteLabels EMITTER
// + the ExtractMetadata deploy READER share one copy). Every charly/ reader below
// references spec.LabelX directly (K3 ZERO-ALIASES dissolution — an alias is always
// residue regardless of what it aliases).

// BoxMetadata + the OCI-label sub-shapes are CUE-sourced in spec (boxmetadata.cue, P2B, #60)
// and aliased IN-PLACE here (spec is the allowed import; these are NOT collected into an
// *_aliases.go file, per the ZERO-ALIASES gate). Field docs + the exact JSON wire tags now live
// on the CUE defs; the ~45 label decoders in ExtractMetadata below still build BoxMetadata
// field-by-field (BoxMetadata is never whole-marshaled — R8 anchor = these sub-shapes' tags).
type (
	LabelVolumeEntry  = spec.LabelVolumeEntry
	LabelRouteEntry   = spec.LabelRouteEntry
	CapabilityService = spec.CapabilityService
	CapabilityInitDef = spec.CapabilityInitDef
	LabelDataEntry    = spec.LabelDataEntry
	BoxMetadata       = spec.BoxMetadata
	LabelShellSet     = spec.LabelShellSet
	ShellEntry        = spec.ShellEntry
)

// InspectLabels reads OCI labels from a local image via engine inspect.
// Package-level var for testability.
var InspectLabels = defaultInspectLabels

func defaultInspectLabels(engine, imageRef string) (map[string]string, error) {
	binary := kit.EngineBinary(engine)
	cmd := exec.Command(binary, "inspect", "--format", "{{json .Config.Labels}}", imageRef)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("inspecting %s: %w", imageRef, err)
	}

	trimmed := strings.TrimSpace(string(output))
	if trimmed == "null" || trimmed == "" {
		return nil, nil
	}

	var labels map[string]string
	if err := json.Unmarshal([]byte(trimmed), &labels); err != nil {
		return nil, fmt.Errorf("parsing labels from %s: %w", imageRef, err)
	}
	return labels, nil
}

// ExtractMetadata reads OCI labels from a local image and returns parsed BoxMetadata.
// Returns nil if the image has no ai.opencharly labels.
// Returns kit.ErrImageNotLocal wrapped with the image ref if the image is not in local storage.
//
//nolint:gocyclo // uniform extraction of ~40 OCI labels (exists→unmarshal→store); flat form is the clearest representation
func ExtractMetadata(engine, imageRef string) (*BoxMetadata, error) {
	labels, err := InspectLabels(engine, imageRef)
	if err != nil {
		if !kit.LocalImageExists(engine, imageRef) {
			return nil, fmt.Errorf("%w: %s", kit.ErrImageNotLocal, imageRef)
		}
		return nil, err
	}

	version := labels[spec.LabelVersion]
	if version == "" {
		// Empty ai.opencharly.version => not an opencharly image (a plain
		// registry base). This is the charly-vs-non-charly boundary, NOT a
		// backward-compat shim: every opencharly image always emits a
		// non-empty EffectiveVersion.
		return nil, nil
	}

	// Schema v4: DNS / AcmeEmail / Engine no longer read from OCI labels —
	// they are deployment choices and flow onto BoxMetadata via
	// MergeDeployOntoMetadata (charly.yml → metadata).
	meta := &BoxMetadata{
		Box:      labels[spec.LabelBox],
		Version:  version,
		Registry: labels[spec.LabelRegistry],
		User:     labels[spec.LabelUser],
		Home:     labels[spec.LabelHome],
		Network:  labels[spec.LabelNetwork],
	}

	// Bootc
	if labels[spec.LabelBootc] == "true" {
		meta.Bootc = true
	}

	// UID
	if v := labels[spec.LabelUID]; v != "" {
		uid, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("parsing %s=%q: %w", spec.LabelUID, v, err)
		}
		meta.UID = uid
	}

	// GID
	if v := labels[spec.LabelGID]; v != "" {
		gid, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("parsing %s=%q: %w", spec.LabelGID, v, err)
		}
		meta.GID = gid
	}

	// Ports
	if v := labels[spec.LabelPort]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.Port); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", spec.LabelPort, err)
		}
	}

	// Volumes
	if v := labels[spec.LabelVolume]; v != "" {
		var labelVols []LabelVolumeEntry
		if err := json.Unmarshal([]byte(v), &labelVols); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", spec.LabelVolume, err)
		}
		for _, lv := range labelVols {
			meta.Volume = append(meta.Volume, deploykit.VolumeMount{
				VolumeName:    "charly-" + meta.Box + "-" + lv.Name,
				ContainerPath: lv.Path,
			})
		}
	}

	// Aliases
	if v := labels[spec.LabelAlias]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.Alias); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", spec.LabelAlias, err)
		}
	}

	// Security
	if v := labels[spec.LabelSecurity]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.Security); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", spec.LabelSecurity, err)
		}
	}

	// Tunnel config is a deploy-time concern — read from charly.yml only.
	// Label is no longer written or read.

	// Env — the label is baked as a JSON OBJECT (deploykit WriteLabels bakes the image's
	// spec.Box.Env map). meta.Env is the []string KEY=VALUE form every deploy
	// consumer expects (ResolveEnvVars, the start/shell deployEnv), so decode the
	// object into a map and convert via envMapToPairs — the exact inverse of the
	// bake, and symmetric with the overlay-merge path (deploy.go). Decoding the
	// object straight into []string was the writer/reader mismatch that failed
	// every image with a box-level env: map (check-box "cannot unmarshal object
	// into []string").
	if v := labels[spec.LabelEnv]; v != "" {
		var envMap map[string]string
		if err := json.Unmarshal([]byte(v), &envMap); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", spec.LabelEnv, err)
		}
		meta.Env = kit.EnvMapToPairs(envMap)
	}

	// Hooks
	if v := labels[spec.LabelHook]; v != "" {
		var hooks HooksConfig
		if err := json.Unmarshal([]byte(v), &hooks); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", spec.LabelHook, err)
		}
		meta.Hook = &hooks
	}

	// VM config + libvirt snippets: removed in the VM hard-cutover. No
	// longer emitted as OCI labels; VM definitions live in vm.yml as
	// `kind: vm` entities.

	// Routes
	if v := labels[spec.LabelRoute]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.Route); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", spec.LabelRoute, err)
		}
	}

	// Init system
	meta.Init = labels[spec.LabelInit]

	// Init definition: build-resolved entrypoint + management surface. Deploy
	// reads this label-first (resolveEntrypointFromMeta / resolveInitDefFromMeta);
	// absent only on images built before the label existed.
	if v := labels[spec.LabelInitDef]; v != "" {
		var idef CapabilityInitDef
		if err := json.Unmarshal([]byte(v), &idef); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", spec.LabelInitDef, err)
		}
		meta.InitDef = &idef
	}

	// ServiceNames: read from init-specific label key
	// The label key is stored as ai.opencharly.service.<init> (e.g., ai.opencharly.service.supervisord)
	if meta.Init != "" {
		svcLabel := "ai.opencharly.service." + meta.Init
		if v := labels[svcLabel]; v != "" {
			if err := json.Unmarshal([]byte(v), &meta.ServiceNames); err != nil {
				return nil, fmt.Errorf("parsing %s: %w", svcLabel, err)
			}
		}
	}

	// Services: full structured per-entry data (LabelService).
	if v := labels[spec.LabelService]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.Service); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", spec.LabelService, err)
		}
	}

	// Candy env vars
	if v := labels[spec.LabelEnvCandy]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.EnvCandy); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", spec.LabelEnvCandy, err)
		}
	}

	// Path append
	if v := labels[spec.LabelPathAppend]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.PathAppend); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", spec.LabelPathAppend, err)
		}
	}

	// Port protocols
	if v := labels[spec.LabelPortProto]; v != "" {
		var protos map[string]string
		if err := json.Unmarshal([]byte(v), &protos); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", spec.LabelPortProto, err)
		}
		// PortProto is now string-keyed (spec reshape, P2B) — the JSON label wire was always a
		// string-keyed object, so this is a direct copy (the former map[int]string + Atoi is gone).
		meta.PortProto = protos
	}

	// Port relay
	if v := labels[spec.LabelPortRelay]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.PortRelay); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", spec.LabelPortRelay, err)
		}
	}

	// Skills
	meta.Skill = labels[spec.LabelSkill]

	// Status and info
	meta.Status = labels[spec.LabelStatus]
	meta.Info = labels[spec.LabelInfo]

	// Acceptance-depth rung (check_level)
	meta.CheckLevel = labels[spec.LabelCheckLevel]

	// Candy versions
	if v := labels[spec.LabelCandyVersion]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.CandyVersion); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", spec.LabelCandyVersion, err)
		}
	}

	// Secrets
	if v := labels[spec.LabelSecret]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.Secret); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", spec.LabelSecret, err)
		}
	}

	// Platform distro (distro identity tags; first match picks bootstrap/format templates)
	if v := labels[spec.LabelPlatformDistro]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.Distro); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", spec.LabelPlatformDistro, err)
		}
	}

	// Platform formats (package formats installed in this image: pac, rpm, pixi, …)
	if v := labels[spec.LabelPlatformFormat]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.BuildFormat); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", spec.LabelPlatformFormat, err)
		}
	}

	// Builder uses (consumer-side routing: format → builder-image name)
	if v := labels[spec.LabelBuilderUse]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.Builder); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", spec.LabelBuilderUse, err)
		}
	}

	// Builder provides (producer-side capability: formats this image can build for others)
	if v := labels[spec.LabelBuilderProvide]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.Build); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", spec.LabelBuilderProvide, err)
		}
	}

	// Data entries (staging paths for deploy-time provisioning)
	if v := labels[spec.LabelDataEntries]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.DataEntries); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", spec.LabelDataEntries, err)
		}
	}

	// Data image flag
	if labels[spec.LabelDataBox] == "true" {
		meta.DataImage = true
	}

	// Env provides (env vars for other containers, templates with {{.ContainerName}})
	if v := labels[spec.LabelEnvProvide]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.EnvProvide); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", spec.LabelEnvProvide, err)
		}
	}

	// Env requires (env vars this image must have)
	if v := labels[spec.LabelEnvRequire]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.EnvRequire); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", spec.LabelEnvRequire, err)
		}
	}

	// Env accepts (env vars this image can optionally use)
	if v := labels[spec.LabelEnvAccept]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.EnvAccept); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", spec.LabelEnvAccept, err)
		}
	}

	// Secret requires (credential-store-backed env vars this image must have)
	if v := labels[spec.LabelSecretRequire]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.SecretRequire); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", spec.LabelSecretRequire, err)
		}
	}

	// Secret accepts (credential-store-backed env vars this image can optionally use)
	if v := labels[spec.LabelSecretAccept]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.SecretAccept); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", spec.LabelSecretAccept, err)
		}
	}

	// MCP provides (MCP servers for other containers, templates with {{.ContainerName}})
	if v := labels[spec.LabelMCPProvide]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.MCPProvide); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", spec.LabelMCPProvide, err)
		}
	}

	// MCP requires (MCP servers this image must have)
	if v := labels[spec.LabelMCPRequire]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.MCPRequire); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", spec.LabelMCPRequire, err)
		}
	}

	// MCP accepts (MCP servers this image can optionally use)
	if v := labels[spec.LabelMCPAccept]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.MCPAccept); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", spec.LabelMCPAccept, err)
		}
	}

	// Shell-init manifest (three-section, candy/box/deploy)
	if v := labels[spec.LabelShell]; v != "" {
		var ss LabelShellSet
		if err := json.Unmarshal([]byte(v), &ss); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", spec.LabelShell, err)
		}
		meta.Shell = &ss
	}

	// Description (three-section plan-shaped self-description)
	if v := labels[spec.LabelDescription]; v != "" {
		var ds kit.LabelDescriptionSet
		if err := json.Unmarshal([]byte(v), &ds); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", spec.LabelDescription, err)
		}
		meta.Description = &ds
	}

	return meta, nil
}
