package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// ErrImageNotLocal is returned when ExtractMetadata is called on an image
// that is not present in the engine's local storage. Deploy-mode commands
// unwrap this sentinel at the error boundary to render a recommendation
// pointing users to `charly box pull`.
var ErrImageNotLocal = errors.New("image not found in local storage")

// OCI label key constants (all namespaced under ai.opencharly.)
const (
	LabelVersion  = "ai.opencharly.version"
	LabelBox      = "ai.opencharly.box"
	LabelRegistry = "ai.opencharly.registry"
	LabelBootc    = "ai.opencharly.bootc"
	LabelUID      = "ai.opencharly.uid"
	LabelGID      = "ai.opencharly.gid"
	LabelUser     = "ai.opencharly.user"
	LabelHome     = "ai.opencharly.home"
	LabelPort     = "ai.opencharly.port"
	LabelVolume   = "ai.opencharly.volume"
	LabelAlias    = "ai.opencharly.alias"
	LabelSecurity = "ai.opencharly.security"
	LabelNetwork  = "ai.opencharly.network"
	// Schema v4: LabelTunnel / LabelDNS / LabelAcmeEmail / LabelEngine
	// removed — these are deployment choices with no image-declaration
	// meaning. Deploy-time values flow through BundleNode →
	// BoxMetadata, not through OCI labels.
	LabelEnv  = "ai.opencharly.env"
	LabelHook = "ai.opencharly.hook"
	// LabelVm + LabelLibvirt: removed in the VM hard-cutover. VM specs
	// now live in vm.yml as `kind: vm` entities; no longer embedded
	// in container image OCI labels.
	LabelRoute = "ai.opencharly.route"
	LabelInit  = "ai.opencharly.init"
	// LabelInitDef — the build-resolved init definition (the runtime-relevant
	// subset of the embedded init: vocabulary entry: container entrypoint,
	// fallback entrypoint, and the in-container service-management surface).
	// Baked at build time so deploy reads the init contract from the image
	// itself instead of re-deriving it from a hardcoded registry. Makes the
	// init system TRUE single-source — including init systems declared ONLY
	// in the embedded init: vocabulary, which now reach runtime via this label.
	LabelInitDef        = "ai.opencharly.init_def"
	LabelEnvCandy       = "ai.opencharly.env_candy"
	LabelPathAppend     = "ai.opencharly.path_append"
	LabelPortProto      = "ai.opencharly.port_proto"
	LabelPortRelay      = "ai.opencharly.port_relay"
	LabelSkill          = "ai.opencharly.skill"
	LabelStatus         = "ai.opencharly.status"
	LabelInfo           = "ai.opencharly.info"
	LabelCandyVersion   = "ai.opencharly.candy_version"
	LabelSecret         = "ai.opencharly.secret"
	LabelPlatformDistro = "ai.opencharly.platform.distro"
	LabelPlatformFormat = "ai.opencharly.platform.format"
	LabelBuilderUse     = "ai.opencharly.builder.use"
	LabelBuilderProvide = "ai.opencharly.builder.provide"
	LabelDataEntries    = "ai.opencharly.data"
	LabelDataBox        = "ai.opencharly.data_box"
	LabelEnvProvide     = "ai.opencharly.env_provide"
	LabelEnvRequire     = "ai.opencharly.env_require"
	LabelEnvAccept      = "ai.opencharly.env_accept"
	LabelSecretAccept   = "ai.opencharly.secret_accept"  // credential-store-backed env vars this image can optionally use
	LabelSecretRequire  = "ai.opencharly.secret_require" // credential-store-backed env vars this image must have
	LabelMCPProvide     = "ai.opencharly.mcp_provide"
	LabelMCPRequire     = "ai.opencharly.mcp_require"
	LabelMCPAccept      = "ai.opencharly.mcp_accept"
	// LabelDescription — three-section plan-shaped self-description for
	// every `kind:` entity the image rolled up. Each section carries one
	// LabeledDescription per contributing entity (candy/box/deploy).
	// Authored inline in YAML under `description:` on each kind; collected
	// via CollectDescriptions following the same base-chain walk as
	// CollectHooks. Subject to a 256 KiB soft cap with narrative truncation.
	LabelDescription = "ai.opencharly.description"
	// LabelService — structured JSON array of CapabilityService (full
	// per-entry spec, not just names). Source-less deploy (`charly bundle from-box`)
	// reads this to reconstruct every service's config without the repo.
	LabelService = "ai.opencharly.service"
	// LabelShell — three-section JSON shell-init manifest.
	// Each section (candy/box/deploy) carries an ordered list of
	// ShellEntry contributions (origin = candy name / "box" / "deploy",
	// id, generic body, per-shell ByShell map). Source of truth for
	// `charly box inspect`, `charly bundle from-box`, and the charly.yml
	// `shell:` overlay merge — same shape as LabelDescription.
	LabelShell = "ai.opencharly.shell"
	// LabelCheckLevel — the per-box acceptance-depth rung (none|build|noagent|
	// agent) authored as BoxConfig.CheckLevel. `charly check run <bed>` reads it
	// from the built image to gate how deep the bed's acceptance runs. See
	// check_level.go for the ladder.
	LabelCheckLevel = "ai.opencharly.check_level"
)

// LabelVolumeEntry represents a volume in the label JSON (short name form).
type LabelVolumeEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// LabelRouteEntry represents a traefik route in the label JSON.
type LabelRouteEntry struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

// CapabilityService is the full structured spec of a single service entry
// baked into an OCI label. Mirrors ServiceEntry's fields plus two origin
// annotations (Init, Candy) so a source-less consumer can reconstruct
// everything `charly bundle` needs without the source repo.
type CapabilityService struct {
	Name             string            `json:"name"`
	Scope            string            `json:"scope,omitempty"`
	Enable           bool              `json:"enable,omitempty"`
	UsePackaged      string            `json:"use_packaged,omitempty"`
	Exec             string            `json:"exec,omitempty"`
	Env              map[string]string `json:"env,omitempty"`
	Restart          string            `json:"restart,omitempty"`
	WorkingDirectory string            `json:"working_directory,omitempty"`
	User             string            `json:"user,omitempty"`
	After            []string          `json:"after,omitempty"`
	Before           []string          `json:"before,omitempty"`
	Stdout           string            `json:"stdout,omitempty"`
	StopTimeout      string            `json:"stop_timeout,omitempty"`
	Kind             string            `json:"kind,omitempty"`
	Events           string            `json:"events,omitempty"`
	AutoStart        *bool             `json:"auto_start,omitempty"`
	StartRetries     int               `json:"start_retries,omitempty"`
	StartSec         int               `json:"start_sec,omitempty"`
	StopSignal       string            `json:"stop_signal,omitempty"`
	ExitCode         string            `json:"exit_code,omitempty"`
	Priority         int               `json:"priority,omitempty"`
	Init             string            `json:"init,omitempty"`  // which init system owns this entry
	Candy            string            `json:"candy,omitempty"` // source candy name
}

// CapabilityInitDef is the runtime-relevant subset of an InitDef baked into
// the ai.opencharly.init_def OCI label. The build-time init: vocabulary
// (charly/charly.yml) carries ~20 build-only fields (detection, stage and
// assembly templates); only these four are consulted at deploy time — the
// container entrypoint, its fallback, and the in-container service-management
// surface. Baking them makes the init system TRUE single-source: deploy
// reads this label instead of re-deriving the contract from a hardcoded
// registry, so init systems declared only in the vocabulary work at runtime too.
type CapabilityInitDef struct {
	Entrypoint         []string          `json:"entrypoint,omitempty"`
	FallbackEntrypoint []string          `json:"fallback_entrypoint,omitempty"`
	ManagementTool     string            `json:"management_tool,omitempty"`
	ManagementCommands map[string]string `json:"management_commands,omitempty"`
}

// LabelDataEntry represents a data mapping stored in the ai.opencharly.data label.
type LabelDataEntry struct {
	Volume  string `json:"volume"`         // target volume name
	Staging string `json:"staging"`        // path inside image (/data/<volume>/[dest/])
	Candy   string `json:"candy"`          // source candy name
	Dest    string `json:"dest,omitempty"` // optional subdirectory within volume
}

// BoxMetadata is the runtime-relevant config extracted from image labels.
type BoxMetadata struct {
	Box       string
	Version   string // ai.opencharly.version — content-derived EffectiveVersion (highest candy version, or the image's dedicated version:)
	Registry  string
	Bootc     bool
	UID       int
	GID       int
	User      string
	Home      string
	Port      []string
	Volume    []VolumeMount
	Alias     []CollectedAlias
	Security  SecurityConfig
	Network   string
	Tunnel    *TunnelYAML // populated from charly.yml overlay (not labels)
	DNS       string
	AcmeEmail string
	Env       []string
	Hook      *HooksConfig
	// Vm / Libvirt: removed in the VM hard-cutover. VM config lives on
	// `kind: vm` entities in vm.yml (VmSpec / LibvirtDomain), not on
	// container image OCI labels.
	Route         []LabelRouteEntry
	Init          string              // active init system name ("supervisord", "systemd", "")
	InitDef       *CapabilityInitDef  // build-resolved init contract (LabelInitDef): entrypoint + management surface; deploy reads it label-first
	Service       []CapabilityService // structured per-entry service specs (LabelService); source-less deploy reads these
	ServiceNames  []string            // per-init service names (LabelInit companion); used by `charly service status/restart`
	EnvCandy      map[string]string
	PathAppend    []string
	Engine        string
	PortProto     map[int]string       // container port -> protocol ("http" or "tcp")
	PortRelay     []int                // ports with socat relay (eth0 -> loopback)
	Skill         string               // skill documentation URL
	Status        string               // effective status (working, testing, broken)
	Info          string               // aggregated status info
	CandyVersion  map[string]string    // candy name -> CalVer version
	Secret        []LabelSecretEntry   // secret requirements (metadata only, no values)
	Distro        []string             // distro identity tags (ai.opencharly.platform.distro)
	BuildFormat   []string             // package formats installed (ai.opencharly.platform.format)
	Builder       map[string]string    // format → builder image (ai.opencharly.builder.use)
	Build         []string             // builder capability: formats this image can build (ai.opencharly.builder.provide)
	DataEntries   []LabelDataEntry     // data staging entries for deploy-time provisioning
	DataImage     bool                 // true if this is a data-only image (FROM scratch)
	EnvProvide    map[string]string    // env vars provided to other containers (service discovery templates)
	EnvRequire    []EnvDependency      // env vars image must have from the environment
	EnvAccept     []EnvDependency      // env vars image can optionally use
	SecretAccept  []EnvDependency      // credential-store-backed env vars image can optionally use
	SecretRequire []EnvDependency      // credential-store-backed env vars image must have
	MCPProvide    []MCPServerYAML      // MCP servers provided to other containers (service discovery templates)
	MCPRequire    []EnvDependency      // MCP servers image must have from the environment
	MCPAccept     []EnvDependency      // MCP servers image can optionally use
	Description   *LabelDescriptionSet // three-section plan-shaped self-description (candy/box/deploy)
	Shell         *LabelShellSet       // three-section (candy/box/deploy) shell-init manifest (2026-05 cutover)
	CheckLevel    string               // acceptance-depth rung (ai.opencharly.check_level): none|build|noagent|agent
}

// LabelShellSet is the three-section JSON manifest carried in
// ai.opencharly.shell. Mirrors LabelDescriptionSet's bucketing — Candy
// holds per-candy contributions (origin = candy name); Box holds
// charly.yml-level shell: declarations; Deploy holds deploy-scope
// defaults baked at build time. The charly.yml `shell:` overlay
// merges into a separate runtime-only set via MergeDeployShell.
type LabelShellSet struct {
	Candy  []ShellEntry `json:"candy,omitempty"`
	Box    []ShellEntry `json:"box,omitempty"`
	Deploy []ShellEntry `json:"deploy,omitempty"`
}

// ShellEntry is one origin's full shell-init contribution. ID is the
// stable handle for charly.yml overlay keying ("<origin>" or
// "<origin>:<shell>"). Origin = candy name / "box" / "deploy".
// Generic body + per-shell ByShell map mirror the in-memory
// ShellConfig struct.
type ShellEntry struct {
	Origin   string                `json:"origin"`
	ID       string                `json:"id,omitempty"`
	Generic  *ShellSpec            `json:"generic,omitempty"`
	ByShell  map[string]*ShellSpec `json:"by_shell,omitempty"`
	Priority int                   `json:"priority,omitempty"`
}

// InspectLabels reads OCI labels from a local image via engine inspect.
// Package-level var for testability.
var InspectLabels = defaultInspectLabels

func defaultInspectLabels(engine, imageRef string) (map[string]string, error) {
	binary := EngineBinary(engine)
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
// Returns ErrImageNotLocal wrapped with the image ref if the image is not in local storage.
//
//nolint:gocyclo // uniform extraction of ~40 OCI labels (exists→unmarshal→store); flat form is the clearest representation
func ExtractMetadata(engine, imageRef string) (*BoxMetadata, error) {
	labels, err := InspectLabels(engine, imageRef)
	if err != nil {
		if !LocalImageExists(engine, imageRef) {
			return nil, fmt.Errorf("%w: %s", ErrImageNotLocal, imageRef)
		}
		return nil, err
	}

	version := labels[LabelVersion]
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
		Box:      labels[LabelBox],
		Version:  version,
		Registry: labels[LabelRegistry],
		User:     labels[LabelUser],
		Home:     labels[LabelHome],
		Network:  labels[LabelNetwork],
	}

	// Bootc
	if labels[LabelBootc] == "true" {
		meta.Bootc = true
	}

	// UID
	if v := labels[LabelUID]; v != "" {
		uid, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("parsing %s=%q: %w", LabelUID, v, err)
		}
		meta.UID = uid
	}

	// GID
	if v := labels[LabelGID]; v != "" {
		gid, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("parsing %s=%q: %w", LabelGID, v, err)
		}
		meta.GID = gid
	}

	// Ports
	if v := labels[LabelPort]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.Port); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelPort, err)
		}
	}

	// Volumes
	if v := labels[LabelVolume]; v != "" {
		var labelVols []LabelVolumeEntry
		if err := json.Unmarshal([]byte(v), &labelVols); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelVolume, err)
		}
		for _, lv := range labelVols {
			meta.Volume = append(meta.Volume, VolumeMount{
				VolumeName:    "charly-" + meta.Box + "-" + lv.Name,
				ContainerPath: lv.Path,
			})
		}
	}

	// Aliases
	if v := labels[LabelAlias]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.Alias); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelAlias, err)
		}
	}

	// Security
	if v := labels[LabelSecurity]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.Security); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelSecurity, err)
		}
	}

	// Tunnel config is a deploy-time concern — read from charly.yml only.
	// Label is no longer written or read.

	// Env — the label is baked as a JSON OBJECT (writeLabels bakes the image's
	// spec.Box.Env map). meta.Env is the []string KEY=VALUE form every deploy
	// consumer expects (ResolveEnvVars, the start/shell deployEnv), so decode the
	// object into a map and convert via envMapToPairs — the exact inverse of the
	// bake, and symmetric with the overlay-merge path (deploy.go). Decoding the
	// object straight into []string was the writer/reader mismatch that failed
	// every image with a box-level env: map (check-box "cannot unmarshal object
	// into []string").
	if v := labels[LabelEnv]; v != "" {
		var envMap map[string]string
		if err := json.Unmarshal([]byte(v), &envMap); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelEnv, err)
		}
		meta.Env = envMapToPairs(envMap)
	}

	// Hooks
	if v := labels[LabelHook]; v != "" {
		var hooks HooksConfig
		if err := json.Unmarshal([]byte(v), &hooks); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelHook, err)
		}
		meta.Hook = &hooks
	}

	// VM config + libvirt snippets: removed in the VM hard-cutover. No
	// longer emitted as OCI labels; VM definitions live in vm.yml as
	// `kind: vm` entities.

	// Routes
	if v := labels[LabelRoute]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.Route); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelRoute, err)
		}
	}

	// Init system
	meta.Init = labels[LabelInit]

	// Init definition: build-resolved entrypoint + management surface. Deploy
	// reads this label-first (resolveEntrypointFromMeta / resolveInitDefFromMeta);
	// absent only on images built before the label existed.
	if v := labels[LabelInitDef]; v != "" {
		var idef CapabilityInitDef
		if err := json.Unmarshal([]byte(v), &idef); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelInitDef, err)
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
	if v := labels[LabelService]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.Service); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelService, err)
		}
	}

	// Candy env vars
	if v := labels[LabelEnvCandy]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.EnvCandy); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelEnvCandy, err)
		}
	}

	// Path append
	if v := labels[LabelPathAppend]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.PathAppend); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelPathAppend, err)
		}
	}

	// Port protocols
	if v := labels[LabelPortProto]; v != "" {
		var protos map[string]string
		if err := json.Unmarshal([]byte(v), &protos); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelPortProto, err)
		}
		meta.PortProto = make(map[int]string)
		for k, v := range protos {
			if p, err := strconv.Atoi(k); err == nil {
				meta.PortProto[p] = v
			}
		}
	}

	// Port relay
	if v := labels[LabelPortRelay]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.PortRelay); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelPortRelay, err)
		}
	}

	// Skills
	meta.Skill = labels[LabelSkill]

	// Status and info
	meta.Status = labels[LabelStatus]
	meta.Info = labels[LabelInfo]

	// Acceptance-depth rung (check_level)
	meta.CheckLevel = labels[LabelCheckLevel]

	// Candy versions
	if v := labels[LabelCandyVersion]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.CandyVersion); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelCandyVersion, err)
		}
	}

	// Secrets
	if v := labels[LabelSecret]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.Secret); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelSecret, err)
		}
	}

	// Platform distro (distro identity tags; first match picks bootstrap/format templates)
	if v := labels[LabelPlatformDistro]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.Distro); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelPlatformDistro, err)
		}
	}

	// Platform formats (package formats installed in this image: pac, rpm, pixi, …)
	if v := labels[LabelPlatformFormat]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.BuildFormat); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelPlatformFormat, err)
		}
	}

	// Builder uses (consumer-side routing: format → builder-image name)
	if v := labels[LabelBuilderUse]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.Builder); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelBuilderUse, err)
		}
	}

	// Builder provides (producer-side capability: formats this image can build for others)
	if v := labels[LabelBuilderProvide]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.Build); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelBuilderProvide, err)
		}
	}

	// Data entries (staging paths for deploy-time provisioning)
	if v := labels[LabelDataEntries]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.DataEntries); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelDataEntries, err)
		}
	}

	// Data image flag
	if labels[LabelDataBox] == "true" {
		meta.DataImage = true
	}

	// Env provides (env vars for other containers, templates with {{.ContainerName}})
	if v := labels[LabelEnvProvide]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.EnvProvide); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelEnvProvide, err)
		}
	}

	// Env requires (env vars this image must have)
	if v := labels[LabelEnvRequire]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.EnvRequire); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelEnvRequire, err)
		}
	}

	// Env accepts (env vars this image can optionally use)
	if v := labels[LabelEnvAccept]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.EnvAccept); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelEnvAccept, err)
		}
	}

	// Secret requires (credential-store-backed env vars this image must have)
	if v := labels[LabelSecretRequire]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.SecretRequire); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelSecretRequire, err)
		}
	}

	// Secret accepts (credential-store-backed env vars this image can optionally use)
	if v := labels[LabelSecretAccept]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.SecretAccept); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelSecretAccept, err)
		}
	}

	// MCP provides (MCP servers for other containers, templates with {{.ContainerName}})
	if v := labels[LabelMCPProvide]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.MCPProvide); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelMCPProvide, err)
		}
	}

	// MCP requires (MCP servers this image must have)
	if v := labels[LabelMCPRequire]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.MCPRequire); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelMCPRequire, err)
		}
	}

	// MCP accepts (MCP servers this image can optionally use)
	if v := labels[LabelMCPAccept]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.MCPAccept); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelMCPAccept, err)
		}
	}

	// Shell-init manifest (three-section, candy/box/deploy)
	if v := labels[LabelShell]; v != "" {
		var ss LabelShellSet
		if err := json.Unmarshal([]byte(v), &ss); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelShell, err)
		}
		meta.Shell = &ss
	}

	// Description (three-section plan-shaped self-description)
	if v := labels[LabelDescription]; v != "" {
		var ds LabelDescriptionSet
		if err := json.Unmarshal([]byte(v), &ds); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelDescription, err)
		}
		meta.Description = &ds
	}

	return meta, nil
}
