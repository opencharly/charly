// Hand-written union / shorthand types for the generated `spec` package.
//
// `cue exp gengotypes` degrades every CUE disjunction to `any` / `map[string]any`
// / an empty struct (the Go type system cannot express a disjunction). For the
// handful of schema defs whose Go shape charly relies on, the matching CUE def
// is annotated `@go(-)` in charly/schema/*.cue (suppressing the lossy generated
// type) and the faithful type is hand-written HERE, in the SAME package, so the
// generated structs reference these by name. These are ported from the
// hand-written charly param types (the source of truth named in each comment);
// keep them in lockstep until WF-B repoints package main onto this package.
//
// STANDALONE: this package builds on its own (`go build ./charly/spec/...`) and
// reaches into NO package-main symbol — every method here is self-contained.
package spec

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// StrMap is the Go shape of #StrMap (`{[string]: #StrVal}`). charly decodes every
// env / var / parameter / oci_label map into map[string]string — yaml.v3 coerces
// an unquoted scalar (`PORT: 8080`) to its literal text, so the map stays
// string→string. Source: the StrMap usage across charly/*.go.
type StrMap = map[string]string

// ---------------------------------------------------------------------------
// Step — #Step (description_spec.go). Exactly one intent keyword + the inline
// (generated) Op. StepKind() enforces exactly-one (a Go cross-field rule).
// ---------------------------------------------------------------------------

// StepKeyword is the intent discriminator on a Step.
type StepKeyword string

const (
	KwRun        StepKeyword = "run"
	KwCheck      StepKeyword = "check"
	KwAgentRun   StepKeyword = "agent-run"
	KwAgentCheck StepKeyword = "agent-check"
	KwInclude    StepKeyword = "include"
)

// stepKeywords lists the valid step discriminators in document order.
var stepKeywords = []StepKeyword{KwRun, KwCheck, KwAgentRun, KwAgentCheck, KwInclude}

// Step is a single plan step. Exactly one of Run/Check/AgentRun/AgentCheck/Include
// is non-empty (validated by StepKind()). The embedded Op (generated) is
// inline-promoted.
type Step struct {
	Run        string `yaml:"run,omitempty"         json:"run,omitempty"`
	Check      string `yaml:"check,omitempty"       json:"check,omitempty"`
	AgentRun   string `yaml:"agent-run,omitempty"   json:"agent-run,omitempty"`
	AgentCheck string `yaml:"agent-check,omitempty" json:"agent-check,omitempty"`
	Include    string `yaml:"include,omitempty"     json:"include,omitempty"`

	Op `yaml:",inline" json:",inline"`
}

// StepKind returns the step's intent keyword and an error if zero or multiple
// keyword discriminators are set.
func (s *Step) StepKind() (StepKeyword, error) {
	set := s.keywordsSet()
	if len(set) == 0 {
		return "", fmt.Errorf("step has no keyword (expected exactly one of: %s)", strings.Join(stepKeywordNames(), ", "))
	}
	if len(set) > 1 {
		names := make([]string, len(set))
		for i, k := range set {
			names[i] = string(k)
		}
		return "", fmt.Errorf("step has multiple keywords (%s); exactly one is required", strings.Join(names, ", "))
	}
	return set[0], nil
}

func stepKeywordNames() []string {
	out := make([]string, len(stepKeywords))
	for i, k := range stepKeywords {
		out[i] = string(k)
	}
	return out
}

func (s *Step) keywordsSet() []StepKeyword {
	var set []StepKeyword
	if s.Run != "" {
		set = append(set, KwRun)
	}
	if s.Check != "" {
		set = append(set, KwCheck)
	}
	if s.AgentRun != "" {
		set = append(set, KwAgentRun)
	}
	if s.AgentCheck != "" {
		set = append(set, KwAgentCheck)
	}
	if s.Include != "" {
		set = append(set, KwInclude)
	}
	return set
}

// KeywordText returns the populated keyword's prose regardless of which
// discriminator holds it.
func (s *Step) KeywordText() string {
	switch {
	case s.Run != "":
		return s.Run
	case s.Check != "":
		return s.Check
	case s.AgentRun != "":
		return s.AgentRun
	case s.AgentCheck != "":
		return s.AgentCheck
	case s.Include != "":
		return s.Include
	}
	return ""
}

// IsAgent reports whether the step is agent-graded (agent-run / agent-check).
func (s *Step) IsAgent() bool { return s.AgentRun != "" || s.AgentCheck != "" }

// ---------------------------------------------------------------------------
// Matcher / MatcherList — #Matcher / #MatcherList (checkspec.go). The custom
// (Un)Marshal logic is ported verbatim.
// ---------------------------------------------------------------------------

// Matcher is a goss-style matcher: a scalar (implicit equality) or an
// operator map ({equals: X}, {contains: […]}, {matches: R}, {lt: N}, …).
type Matcher struct {
	Op    string `json:"op"`
	Value any    `json:"value,omitempty"`
}

// MarshalYAML emits the canonical operator-map form so round-tripping is stable.
func (m Matcher) MarshalYAML() (any, error) { //nolint:unparam // error return kept for interface/API stability
	if m.Op == "equals" {
		return m.Value, nil
	}
	return map[string]any{m.Op: m.Value}, nil
}

// MarshalJSON makes the JSON write path the exact inverse of UnmarshalJSON. A ZERO
// (absent) matcher — Op=="" — marshals to `null`, which UnmarshalJSON maps back to the
// zero value; without this, the default struct marshal emitted `{"op":""}`, which
// UnmarshalJSON's single-key-map branch misread as Op="op" (an asymmetry that corrupted
// every zero matcher round-tripped through JSON — e.g. the all-zero check-matcher fields
// of an install Op carried in the per-step IR, or a baked-label Plan step). A NON-zero
// matcher marshals to the SAME canonical `{"op":...,"value":...}` form the default
// marshaler produced, so real matchers (and existing baked labels) are byte-unchanged.
func (m Matcher) MarshalJSON() ([]byte, error) {
	if m.Op == "" {
		return []byte("null"), nil
	}
	type alias Matcher // break the MarshalJSON recursion; emit the canonical struct form
	return json.Marshal(alias(m))
}

// UnmarshalJSON accepts the canonical `{"op":"...","value":...}` form, an
// operator-map (`{"equals": X}`), a bare scalar (`"PONG"` → equals), and `null` /
// empty (→ the zero matcher, the inverse of MarshalJSON's zero case).
func (m *Matcher) UnmarshalJSON(data []byte) error {
	s := bytes.TrimSpace(data)
	if len(s) == 0 || string(s) == "null" {
		return nil
	}
	if s[0] == '{' {
		var tmp struct {
			Op    string `json:"op"`
			Value any    `json:"value"`
		}
		if err := json.Unmarshal(data, &tmp); err == nil && tmp.Op != "" {
			m.Op = tmp.Op
			m.Value = tmp.Value
			return nil
		}
		var raw map[string]any
		if err := json.Unmarshal(data, &raw); err != nil {
			return err
		}
		if len(raw) != 1 {
			return fmt.Errorf("matcher map must have exactly one operator key, got %d", len(raw))
		}
		for k, v := range raw {
			m.Op = k
			m.Value = v
		}
		return nil
	}
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	m.Op = "equals"
	m.Value = v
	return nil
}

// MatcherList lets users write scalar/single-map shorthand where a list of
// Matchers is expected. `stdout: PONG` and `stdout: [PONG]` decode identically.
type MatcherList []Matcher

// UnmarshalJSON mirrors the scalar-shorthand behavior for the JSON read path.
func (ml *MatcherList) UnmarshalJSON(data []byte) error {
	s := bytes.TrimSpace(data)
	if len(s) == 0 {
		*ml = nil
		return nil
	}
	if s[0] == '[' {
		var list []Matcher
		if err := json.Unmarshal(data, &list); err != nil {
			return err
		}
		*ml = list
		return nil
	}
	var m Matcher
	if err := m.UnmarshalJSON(data); err != nil {
		return err
	}
	*ml = []Matcher{m}
	return nil
}

// (The former ContainsList — a MatcherList variant whose bare scalars defaulted to
// Op="contains" — left the base schema with the `file` verb's `contains` field. It is
// now reproduced standalone in the file plugin's #FileContains and decoded with that
// default via decodeContainsList; no base #Op field uses it anymore.)

// ---------------------------------------------------------------------------
// PackageItem — #PackageItem (calamares_types.go). Bare scalar XOR object form;
// the bare-scalar shorthand is canonicalized to {name} by the loader normalizer.
// ---------------------------------------------------------------------------
type PackageItem struct {
	Name        string `yaml:"name" json:"name"`
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
}

// ---------------------------------------------------------------------------
// VmSource / VmSSH — #VmSource / #VmSSH. VmSource is the flat
// discriminated-union source; VmSSH references the generated VmKeyInjection.
// ---------------------------------------------------------------------------

// VmSource is the discriminated-union source for a VM disk image (Kind selects
// the active branch: cloud_image / bootc / clone / imported / bootstrap).
type VmSource struct {
	Kind             string     `yaml:"kind" json:"kind"`
	URL              string     `yaml:"url,omitempty" json:"url,omitempty"`
	Checksum         VmChecksum `yaml:"checksum,omitempty" json:"checksum,omitempty"`
	Cache            string     `yaml:"cache,omitempty" json:"cache,omitempty"`
	BaseUser         string     `yaml:"base_user,omitempty" json:"base_user,omitempty"`
	Box              string     `yaml:"box,omitempty" json:"box,omitempty"`
	Transport        string     `yaml:"transport,omitempty" json:"transport,omitempty"`
	Rootfs           string     `yaml:"rootfs,omitempty" json:"rootfs,omitempty"`
	RootSize         string     `yaml:"root_size,omitempty" json:"root_size,omitempty"`
	KernelArgs       string     `yaml:"kernel_args,omitempty" json:"kernel_args,omitempty"`
	FromVm           string     `yaml:"from_vm,omitempty" json:"from_vm,omitempty"`
	FromSnapshot     string     `yaml:"from_snapshot,omitempty" json:"from_snapshot,omitempty"`
	CloudInitClean   bool       `yaml:"cloud_init_clean,omitempty" json:"cloud_init_clean,omitempty"`
	LibvirtName      string     `yaml:"libvirt_name,omitempty" json:"libvirt_name,omitempty"`
	DiskPath         string     `yaml:"disk_path,omitempty" json:"disk_path,omitempty"`
	DiskFormat       string     `yaml:"disk_format,omitempty" json:"disk_format,omitempty"`
	AdoptedAt        string     `yaml:"adopted_at,omitempty" json:"adopted_at,omitempty"`
	LastSyncedAt     string     `yaml:"last_synced_at,omitempty" json:"last_synced_at,omitempty"`
	Builder          string     `yaml:"builder,omitempty" json:"builder,omitempty"`
	BuilderImage     string     `yaml:"builder_image,omitempty" json:"builder_image,omitempty"`
	Distro           string     `yaml:"distro,omitempty" json:"distro,omitempty"`
	Package          []string   `yaml:"package,omitempty" json:"package,omitempty"`
	BootstrapArch    string     `yaml:"bootstrap_arch,omitempty" json:"bootstrap_arch,omitempty"`
	BootstrapVariant string     `yaml:"bootstrap_variant,omitempty" json:"bootstrap_variant,omitempty"`
}

// VmSSH is the guest SSH access config (port XOR port_auto is a Go cross-field
// rule). KeyInjection references the generated VmKeyInjection.
type VmSSH struct {
	User         string          `yaml:"user,omitempty" json:"user,omitempty"`
	Port         int             `yaml:"port,omitempty" json:"port,omitempty"`
	PortAuto     bool            `yaml:"port_auto,omitempty" json:"port_auto,omitempty"`
	KeySource    string          `yaml:"key_source,omitempty" json:"key_source,omitempty"`
	KeyInjection *VmKeyInjection `yaml:"key_injection,omitempty" json:"key_injection,omitempty"`
}

// ---------------------------------------------------------------------------
// ApkPackageSpec — #CandyApk (android_spec.go). package XOR apk.
// ---------------------------------------------------------------------------
type ApkPackageSpec struct {
	Package    string `yaml:"package,omitempty" json:"package,omitempty"`
	Apk        string `yaml:"apk,omitempty" json:"apk,omitempty"`
	Source     string `yaml:"source,omitempty" json:"source,omitempty"`
	Arch       string `yaml:"arch,omitempty" json:"arch,omitempty"`
	AppVersion string `yaml:"app_version,omitempty" json:"app_version,omitempty"`
}

// ---------------------------------------------------------------------------
// LibvirtHostdev — #LibvirtHostdev (libvirt_yaml.go). The `if type=="pci"`
// source-redefine degrades the generated def to `any`.
// ---------------------------------------------------------------------------
type LibvirtHostdev struct {
	Type    string            `yaml:"type" json:"type"`
	Mode    string            `yaml:"mode,omitempty" json:"mode,omitempty"`
	Managed string            `yaml:"managed,omitempty" json:"managed,omitempty"`
	Source  map[string]string `yaml:"source" json:"source"`
	ROM     map[string]string `yaml:"rom,omitempty" json:"rom,omitempty"`
	Driver  map[string]string `yaml:"driver,omitempty" json:"driver,omitempty"`
}

// ---------------------------------------------------------------------------
// LibvirtGraphicsListen / LibvirtGraphicsListeners — #LibvirtListen
// (libvirt_yaml_listen.go). scalar | map | list of listeners.
// ---------------------------------------------------------------------------
type LibvirtGraphicsListen struct {
	Type    string `yaml:"type,omitempty" json:"type,omitempty"`
	Address string `yaml:"address,omitempty" json:"address,omitempty"`
	Network string `yaml:"network,omitempty" json:"network,omitempty"`
	Socket  string `yaml:"socket,omitempty" json:"socket,omitempty"`
}

// LibvirtGraphicsListeners is the YAML-shaped list of listeners for one
// <graphics> element.
type LibvirtGraphicsListeners []LibvirtGraphicsListen

// First returns the first listener, or the zero value if empty.
func (ll LibvirtGraphicsListeners) First() LibvirtGraphicsListen {
	if len(ll) == 0 {
		return LibvirtGraphicsListen{}
	}
	return ll[0]
}

// ---------------------------------------------------------------------------
// PortScope / TunnelYAML — #PortScope / #Tunnel (tunnel.go).
// ---------------------------------------------------------------------------

// PortScope handles three YAML forms for public/private port specification:
// "all" → All; [443, 8443] → Ports; {18789: "host"} → PortMap.
type PortScope struct {
	All     bool
	Ports   []int
	PortMap map[int]string // port → hostname (cloudflare only)
}

func (p *PortScope) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		if value.Value == "all" {
			p.All = true
			return nil
		}
		return fmt.Errorf("expected 'all', port list, or port map, got %q", value.Value)
	case yaml.SequenceNode:
		return value.Decode(&p.Ports)
	case yaml.MappingNode:
		p.PortMap = make(map[int]string)
		return value.Decode(&p.PortMap)
	}
	return fmt.Errorf("unexpected YAML node type for port scope")
}

func (p PortScope) MarshalJSON() ([]byte, error) {
	if p.All {
		return json.Marshal("all")
	}
	if len(p.PortMap) > 0 {
		return json.Marshal(p.PortMap)
	}
	if len(p.Ports) > 0 {
		return json.Marshal(p.Ports)
	}
	return []byte("null"), nil
}

func (p PortScope) MarshalYAML() (any, error) { //nolint:unparam // error return kept for interface/API stability
	if p.All {
		return "all", nil
	}
	if len(p.PortMap) > 0 {
		return p.PortMap, nil
	}
	if len(p.Ports) > 0 {
		return p.Ports, nil
	}
	return nil, nil
}

func (p *PortScope) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		return nil
	}
	var s string
	if json.Unmarshal(data, &s) == nil && s == "all" {
		p.All = true
		return nil
	}
	var ports []int
	if json.Unmarshal(data, &ports) == nil {
		p.Ports = ports
		return nil
	}
	var raw map[string]string
	if err := json.Unmarshal(data, &raw); err == nil {
		p.PortMap = make(map[int]string, len(raw))
		for k, v := range raw {
			port, err := strconv.Atoi(k)
			if err != nil {
				return fmt.Errorf("invalid port number %q in port map: %w", k, err)
			}
			p.PortMap[port] = v
		}
		return nil
	}
	return fmt.Errorf("cannot unmarshal port scope from JSON: %s", string(data))
}

func (p PortScope) IsZero() bool {
	return !p.All && len(p.Ports) == 0 && len(p.PortMap) == 0
}

// TunnelYAML supports both bare string and expanded form (the `tunnel:` field).
type TunnelYAML struct {
	Provider string    `yaml:"provider" json:"provider"`
	Tunnel   string    `yaml:"tunnel,omitempty" json:"tunnel,omitempty"`
	Public   PortScope `yaml:"public,omitempty" json:"public"`
	Private  PortScope `yaml:"private,omitempty" json:"private"`
}

// ---------------------------------------------------------------------------
// EphemeralLifetime / PreemptibleConfig — #Ephemeral / #Preemptible (deploy.go).
// EphemeralLifetime self-decodes the boolean-shorthand (`ephemeral: true`).
// ---------------------------------------------------------------------------

// EphemeralLifetime parameterizes the auto-destruction lifecycle for a deploy.
type EphemeralLifetime struct {
	TTL           string `yaml:"ttl,omitempty" json:"ttl,omitempty"`
	KeepOnFailure bool   `yaml:"keep_on_failure,omitempty" json:"keep_on_failure,omitempty"`
	NamingPattern string `yaml:"naming_pattern,omitempty" json:"naming_pattern,omitempty"`

	// boolForm captures whether YAML authored the field as a bare boolean
	// (`ephemeral: true`) vs a block.
	boolForm bool
}

// UnmarshalJSON accepts the boolean shorthand and the block form (the loader is
// JSON-based via cue.Value.Decode). `ephemeral: false` is rejected.
func (e *EphemeralLifetime) UnmarshalJSON(data []byte) error {
	s := strings.TrimSpace(string(data))
	if s == "" || s == "null" {
		return nil
	}
	if s[0] == '{' {
		var raw struct {
			TTL           string `json:"ttl"`
			KeepOnFailure bool   `json:"keep_on_failure"`
			NamingPattern string `json:"naming_pattern"`
		}
		if err := json.Unmarshal(data, &raw); err != nil {
			return fmt.Errorf("ephemeral block: %w", err)
		}
		e.TTL = raw.TTL
		e.KeepOnFailure = raw.KeepOnFailure
		e.NamingPattern = raw.NamingPattern
		e.boolForm = false
		return nil
	}
	var b bool
	if err := json.Unmarshal(data, &b); err == nil {
		if !b {
			return fmt.Errorf("ephemeral: false is not supported — omit the field instead (or set ephemeral: true / ephemeral: {block})")
		}
		e.boolForm = true
		return nil
	}
	var str string
	if err := json.Unmarshal(data, &str); err == nil {
		switch strings.ToLower(strings.TrimSpace(str)) {
		case "true", "yes", "on":
			e.boolForm = true
			return nil
		case "false", "no", "off", "":
			return fmt.Errorf("ephemeral: false is not supported — omit the field instead")
		}
	}
	return fmt.Errorf("ephemeral: value %s is not a boolean or block", s)
}

// IsBoolForm reports whether the field was authored as a bare boolean.
func (e *EphemeralLifetime) IsBoolForm() bool { return e != nil && e.boolForm }

// PreemptibleConfig is the HOLDER side of the resource-arbitration axis.
type PreemptibleConfig struct {
	Holds   []string `yaml:"holds,omitempty" json:"holds,omitempty"`
	Stop    string   `yaml:"stop,omitempty" json:"stop,omitempty"`
	Restore string   `yaml:"restore,omitempty" json:"restore,omitempty"`
}
