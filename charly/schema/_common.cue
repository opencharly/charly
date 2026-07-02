// Shared CUE definitions referenced by multiple kinds. R3: define each shared
// shape ONCE here, not per-kind. All schema/*.cue files compile into one
// instance (no package clauses), so any kind def can reference these directly.

// Execution context for a plan step.
#Context: ("build" | "deploy" | "runtime") @go(-)

// #OpVerb — the verb DISCRIMINATOR vocabulary: the subset of #Op fields that are
// VERBS (exactly-one-set discriminators), as opposed to the shared modifiers
// (to/mode/content/timeout/…) that decorate them. #Op alone cannot express the
// verb-vs-modifier split (every field is structurally a string/int), so this enum
// is the ONE CUE source naming the verbs — schemagen emits it as spec.OpVerbs
// (the canonical verb set for Op.Kind()'s exactly-one-verb error + the package-main
// VerbCatalog dispatch table, both gated against it). Every name MUST be an #Op
// field (the schemagen gate "OpVerbs ⊆ AuthoringVerbs" proves it) and MUST have a
// VerbCatalog entry (the registry bijection gate proves it). Keep in lockstep with
// the `--- verb discriminators ---` group in #Op.
#OpVerb: ("mkdir" | "copy" | "write" | "link" | "download" | "setcap" | "build" |
	"summarize" | "kill" | "plugin") @go(-)

// ---------------------------------------------------------------------------
// Plan steps: the unified run/check/agent-run/agent-check/include vocabulary.
// ---------------------------------------------------------------------------

// One flat plan step: exactly ONE intent keyword (run / check / agent-run /
// agent-check / include) carrying prose, plus an inline Op (verb + modifiers).
// Exactly-one-keyword is the discriminated-union idiom — each arm requires its
// keyword and forbids the other four via _|_. Each arm EMBEDS the closed #Op,
// so the Op modifier field set is now CLOSED too (an unknown key is a typo).
// Op verb-exclusivity (Kind()) stays a Go per-entity check; CUE closes the
// field set, types every field, and enumerates every live-verb method.
// Every arm also forbids the runtime-derived venue/intent_do (stamped post-decode,
// never authored — see #Op).
#Step:
	{#Op, run: string & !="", check?: _|_, "agent-run"?: _|_, "agent-check"?: _|_, include?: _|_, venue?: _|_, intent_do?: _|_} |
	{#Op, check: string & !="", run?: _|_, "agent-run"?: _|_, "agent-check"?: _|_, include?: _|_, venue?: _|_, intent_do?: _|_} |
	{#Op, "agent-run": string & !="", run?: _|_, check?: _|_, "agent-check"?: _|_, include?: _|_, venue?: _|_, intent_do?: _|_} |
	{#Op, "agent-check": string & !="", run?: _|_, check?: _|_, "agent-run"?: _|_, include?: _|_, venue?: _|_, intent_do?: _|_} |
	{#Op, include: string & !="", run?: _|_, check?: _|_, "agent-run"?: _|_, "agent-check"?: _|_, venue?: _|_, intent_do?: _|_} @go(-) // gengotypes: hand-written Step (spec/union_types.go) embeds the generated Op

// #Op is the unified operation vocabulary (Go: charly/checkspec.go Op). CLOSED:
// every authored yaml key is modeled — an unknown key is a typo. Verb
// discriminators are optional (exactly-one is Go's Kind(), a cross-field rule
// kept in Go); CUE closes the set, types each field, and enumerates the
// live-verb methods + matcher operators. yaml:"-" fields (intentDo, origin) are
// never authored and intentionally absent.
#Op: {
	// --- verb discriminators (exactly one set; Go Kind() enforces) ---
	mkdir?:          string
	copy?:           string
	write?:          string
	link?:           string
	download?:       string
	setcap?:         string
	build?:          string
	// `cdp` is an EXTERNAL-CHARLY-VERB: its Chrome DevTools Protocol client (the
	// golang.org/x/net/websocket CDP WebSocket client + the open/list/text/eval/screenshot/
	// click/SPA dial+dispatch layer) lives in the out-of-tree candy/plugin-cdp module,
	// served OUT-OF-PROCESS. Like mcp/vnc/spice (and unlike file/package/service/command,
	// which left #Op entirely, re-authored as `plugin: <verb>`), cdp KEEPS its `cdp:`
	// discriminator + every modifier (tab/url/expression/selector/…) on this closed #Op —
	// authoring is unchanged (`cdp: status`, not `plugin: cdp`). It therefore left
	// #OpVerb/spec.OpVerbs/VerbCatalog (no in-proc CheckVerbProvider to gate) BUT keeps this
	// field + #CdpMethod here, so `cdp: status` still validates against the method enum and
	// VerbsSet still classifies the op (then dispatch resolves the registered external
	// provider, after the host pre-resolves the deployment's CDP port to a DevTools URL).
	cdp?:            #CdpMethod
	// `wl` is an EXTERNAL-CHARLY-VERB: its Wayland/sway desktop driver (input, windows,
	// screenshots, sway IPC, overlay, atspi, clipboard — ~40 methods) lives in the out-of-tree
	// candy/plugin-wl module, served OUT-OF-PROCESS. Like cdp/vnc/mcp/record/dbus (and unlike
	// file/package/service/command, which left #Op entirely, re-authored as `plugin: <verb>`),
	// wl KEEPS its `wl:` discriminator + every modifier
	// (x/y/x2/y2/direction/amount/target/text/key/combo/command/action/query/artifact) on this
	// closed #Op — authoring is unchanged (`wl: screenshot`, not `plugin: wl`). It therefore left
	// #OpVerb/spec.OpVerbs/VerbCatalog (no in-proc CheckVerbProvider to gate) BUT keeps this field
	// + #WlMethod here, so `wl: screenshot` still validates against the method enum and VerbsSet
	// still classifies the op (then dispatch resolves the registered external provider, which
	// drives the venue's compositor over the executor reverse channel — wl is EXEC-based, like
	// record/dbus; the screenshot PNG pulls via GetFile). wl is the LAST live-container verb to
	// leave charly's core: after it, ZERO check verbs are compiled-in.
	wl?:             #WlMethod
	// `dbus` is an EXTERNAL-CHARLY-VERB: its D-Bus driver (list/call/introspect/notify against
	// the venue's session bus) lives in the out-of-tree candy/plugin-dbus module, served
	// OUT-OF-PROCESS. Like cdp/vnc/mcp/record (and unlike file/package/service/command, which
	// left #Op entirely, re-authored as `plugin: <verb>`), dbus KEEPS its `dbus:` discriminator
	// + every modifier (dest/path/method/args/text/description) on this closed #Op — authoring
	// is unchanged (`dbus: list`, not `plugin: dbus`). It therefore left
	// #OpVerb/spec.OpVerbs/VerbCatalog (no in-proc CheckVerbProvider to gate) BUT keeps this
	// field + #DbusMethod here, so `dbus: list` still validates against the method enum and
	// VerbsSet still classifies the op (then dispatch resolves the registered external provider,
	// which drives the venue's session bus with gdbus over the executor reverse channel — dbus
	// is EXEC-based, like record). STRUCTURAL externalization, not a dep-shed: dbus drives the
	// venue bus with gdbus, never godbus.
	dbus?:           #DbusMethod
	// `vnc` is an EXTERNAL-CHARLY-VERB: its RFB/VNC client (the stdlib-only RFC 6143 VNC
	// client — VeNCrypt/TLS + ZRLE decode + the status/screenshot/click/mouse/type/key/rfb
	// dispatch layer) lives in the out-of-tree candy/plugin-vnc module, served
	// OUT-OF-PROCESS. Like cdp/mcp/spice (and unlike file/package/service/command, which
	// left #Op entirely, re-authored as `plugin: <verb>`), vnc KEEPS its `vnc:` discriminator
	// + every modifier (x/y/text/key/artifact/method/params) on this closed #Op — authoring
	// is unchanged (`vnc: status`, not `plugin: vnc`). It therefore left
	// #OpVerb/spec.OpVerbs/VerbCatalog (no in-proc CheckVerbProvider to gate) BUT keeps this
	// field + #VncMethod here, so `vnc: status` still validates against the method enum and
	// VerbsSet still classifies the op (then dispatch resolves the registered external
	// provider, after the host pre-resolves the deployment's VNC endpoint — container port
	// 5900 or a VM's libvirt display — to a host-reachable RFB address).
	vnc?:            #VncMethod
	// `mcp` is an EXTERNAL-CHARLY-VERB: its MCP-protocol client implementation (the
	// github.com/modelcontextprotocol/go-sdk client + the dial/dispatch/format layer)
	// lives in the out-of-tree candy/plugin-mcp module, served OUT-OF-PROCESS. Like
	// cdp/vnc/spice (and unlike file/package/service/command, which left #Op entirely,
	// re-authored as `plugin: <verb>`), mcp KEEPS its `mcp:` discriminator + every
	// modifier (mcp_name/tool/uri/input) on this closed #Op — authoring is unchanged
	// (`mcp: ping`, not `plugin: mcp`). It therefore left #OpVerb/spec.OpVerbs/VerbCatalog
	// (no in-proc CheckVerbProvider to gate) BUT keeps this field + #McpMethod here, so
	// `mcp: ping` still validates against the method enum and VerbsSet still classifies the
	// op (then dispatch resolves the registered external provider, after the host
	// pre-resolves the deployment's declared mcp_provides + the picked dial endpoint).
	mcp?:            #McpMethod
	// `record` is an EXTERNAL-CHARLY-VERB: its recording driver (the asciinema/wf-recorder/
	// pixelflux session management) lives in the out-of-tree candy/plugin-record module,
	// served OUT-OF-PROCESS. Like cdp/vnc/mcp/spice (and unlike file/package/service/command,
	// which left #Op entirely, re-authored as `plugin: <verb>`), record KEEPS its `record:`
	// discriminator + every modifier (record_name/record_mode/record_fps/record_audio) on
	// this closed #Op — authoring is unchanged (`record: start`, not `plugin: record`). It
	// therefore left #OpVerb/spec.OpVerbs/VerbCatalog (no in-proc CheckVerbProvider to gate)
	// BUT keeps this field + #RecordMethod here, so `record: start` still validates against
	// the method enum and VerbsSet still classifies the op (then dispatch resolves the
	// registered external provider, which drives the venue over the executor reverse channel —
	// record is the FIRST EXEC-based external verb).
	record?:         #RecordMethod
	// `spice` is an EXTERNAL-CHARLY-VERB: its SPICE-wire implementation (+ the upstream
	// SPICE wire client library and its cgo opus/portaudio audio transitives, vendored
	// under candy/plugin-spice/third_party) lives in the out-of-tree candy/plugin-spice
	// module, served OUT-OF-PROCESS. Like cdp/vnc (and unlike file/package/service/command, which
	// left #Op entirely, re-authored as `plugin: <verb>`), spice KEEPS its `spice:`
	// discriminator + every modifier on this closed #Op — authoring is unchanged
	// (`spice: status`, not `plugin: spice`). It therefore left
	// #OpVerb/spec.OpVerbs/VerbCatalog (no in-proc CheckVerbProvider to gate) BUT keeps
	// this field + #SpiceMethod here, so `spice: status` still validates against the
	// method enum and VerbsSet still classifies the op (then dispatch resolves the
	// registered external provider, after the host pre-resolves the VM's live SPICE
	// endpoint to a dialable address — the plugin needs no go-libvirt).
	spice?:          #SpiceMethod
	// `libvirt` is an EXTERNAL-CHARLY-VERB: its go-libvirt + kata-containers/govmm +
	// libvirt.org/go/libvirtxml VM implementation lives in the out-of-tree candy/plugin-vm module,
	// served OUT-OF-PROCESS. Like spice/kube/adb (and unlike file/package/service/command, which
	// left #Op entirely, re-authored as `plugin: <verb>`), libvirt KEEPS its `libvirt:` discriminator
	// + every #LibvirtMethod modifier on this closed #Op — authoring is unchanged (`libvirt: list`,
	// not `plugin: libvirt`). It therefore left #OpVerb/spec.OpVerbs/VerbCatalog (no in-proc
	// CheckVerbProvider to gate) BUT keeps this field + #LibvirtMethod here, so `libvirt: list` still
	// validates against the method enum and VerbsSet still classifies the op (then dispatch resolves
	// the registered external provider; the host pre-resolves any VM display endpoint host-side).
	libvirt?:        #LibvirtMethod
	// `kube` is an EXTERNAL-CHARLY-VERB: its implementation (+ the heavy
	// client-go + apimachinery dependency) lives in the out-of-tree
	// candy/plugin-kube module, served OUT-OF-PROCESS. Like cdp/vnc (and unlike
	// file/package/service/command, which left #Op entirely, re-authored as
	// `plugin: <verb>`), kube KEEPS its `kube:` discriminator + every modifier on
	// this closed #Op — authoring is unchanged (`kube: nodes`, not `plugin: kube`).
	// It therefore left #OpVerb/spec.OpVerbs/VerbCatalog (no in-proc CheckVerbProvider
	// to gate) BUT keeps this field + #KubeMethod here, so `kube: nodes` still
	// validates against the method enum and VerbsSet still classifies the op (then
	// dispatch resolves the registered external provider, after the host pre-resolves
	// any --cluster profile to a concrete kubeconfig context).
	kube?:           #KubeMethod
	// `adb` is an EXTERNAL-CHARLY-VERB: its implementation (+ the heavy goadb
	// ADB-wire dependency) lives in the out-of-tree candy/plugin-adb module,
	// served OUT-OF-PROCESS. Like cdp/vnc (and unlike
	// file/package/service/command, which left #Op entirely, re-authored as
	// `plugin: <verb>`), adb KEEPS its `adb:` discriminator + every modifier on
	// this closed #Op — authoring is unchanged (`adb: devices`, not `plugin: adb`).
	// It therefore left #OpVerb/spec.OpVerbs/VerbCatalog (no in-proc CheckVerbProvider
	// to gate) BUT keeps this field + #AdbMethod here, so `adb: devices` still
	// validates against the method enum and VerbsSet still classifies the op (then
	// dispatch resolves the registered external provider).
	adb?:            #AdbMethod
	// `appium` is an EXTERNAL-CHARLY-VERB: its implementation (+ the heavy
	// github.com/tebeka/selenium dependency) lives in the out-of-tree
	// candy/plugin-appium module, served OUT-OF-PROCESS. Unlike file/package/
	// service/command (which left #Op entirely, re-authored as `plugin: <verb>`),
	// appium KEEPS its `appium:` discriminator + every modifier on this closed #Op —
	// authoring is unchanged. It therefore left #OpVerb/spec.OpVerbs/VerbCatalog (no
	// in-proc CheckVerbProvider to gate) BUT keeps this field + #AppiumMethod here, so
	// `appium: status` still validates against the method enum and VerbsSet still
	// classifies the op (then dispatch resolves the registered external provider).
	appium?:         #AppiumMethod
	summarize?:      string
	kill?:           string
	signal?:         "TERM" | "KILL"
	// plugin — the generic PLUGIN-VERB discriminator. Its value is a reserved word
	// served by a registered Provider (built-in or out-of-tree plugin); the host
	// #Op cannot type per-plugin verb fields (an external plugin's vocabulary is
	// not compiled in), so a plugin verb is authored via this generic envelope and
	// dispatched through providerRegistry.ResolveVerb. plugin_input carries the
	// params, validated by the PLUGIN's own spliced CUE schema (not base #Op).
	plugin?: string

	// --- shared resource-identity modifiers ---
	name?:      string
	namespace?: string
	label?:     string
	cluster?:   string
	manifest?:  string

	// --- kube-specific modifiers ---
	kube_kind?:     string @go(KubeKind)
	kube_context?:  string @go(KubeContext)
	kubeconfig?:    string
	kube_count?:    int    @go(KubeCount,type=int)
	kube_resource?: string @go(KubeResource)
	kube_group?:    string @go(KubeGroup)
	kube_version?:  string @go(KubeVersion)
	json?:          bool   @go(JSON)

	// --- shared modifiers ---
	id?:          string @go(ID)
	description?: string
	skip?:        bool
	timeout?:     #Duration
	// command — a SHARED exec-string modifier (NOT a verb): the live-container verbs
	// `wl: exec` / `wl: sway-msg` / `libvirt: guest/exec` read it as their argv, and
	// the `command` plugin verb's INSTALL-EMIT rehydrates it onto an OpStep for emitCmd
	// (build) / renderOpCommand (deploy). It LEFT #OpVerb in the command→plugin
	// extraction (the command CHECK verb is now `plugin: command` + #CommandInput), so
	// Op.Kind() no longer treats it as a verb; it stays here as a modifier the other
	// verbs + the act-emit seam read off the step Op. `in_container`/`background`/
	// `from_host` were command-EXCLUSIVE and moved into #CommandInput.
	command?: string
	context?: [...#Context]
	// `pod:` (per-step container venue) is RETIRED — a step's execution venue is
	// derived ENTIRELY from its position in the bundle tree (flattenBundleVenues
	// → Op.venue, yaml:"-"). Authoring it is a closed-schema rejection (run:
	// charly migrate).
	depends_on?: [...string] @go(DependsOn)
	// plugin_input — generic params for a `plugin:` verb. Opaque to base #Op
	// (accepts any shape); the plugin's own spliced CUE schema validates it.
	plugin_input?: {...} @go(PluginInput,type=map[string]any)

	// --- install/build modifiers ---
	run_as?:  string @go(RunAs)
	to?:      string
	content?: string
	extract?: "tar.gz" | "tar.xz" | "tar.zst" | "zip" | "none" | "sh" | ""
	extract_include?: [...string] @go(ExtractInclude)
	strip_components?: int & >=0 @go(StripComponents,type=int)
	uninstall?: [...string]
	comment?: string
	cache?: [...string]
	env?: #StrMap

	// --- step modifiers ---
	capture?:         string
	capture_extract?: string @go(CaptureExtract)
	eventually?:      #Duration
	retry_interval?:  #Duration @go(RetryInterval)
	// `on:` (cross-member driver dispatch) is RETIRED — a step that drives a
	// peer/driver is authored as a step CHILD of that member node; its venue is
	// derived from position (flattenBundleVenues → Op.venue). Authoring `on:` is
	// a closed-schema rejection (run: charly migrate).
	tag?: [...string]

	// --- concurrency ---
	parallel?:  string
	count?:     int & >=0 @go(,type=int)
	index_var?: string    @go(IndexVar)

	// --- aggregation (summarize) ---
	over_id?: [...string] @go(OverIDs)
	metric?: [...string] @go(Metrics)
	emit_id?: string   @go(EmitID)
	p50?:     #Matcher @go(P50Match)
	p95?:     #Matcher @go(P95Match)
	p99?:     #Matcher @go(P99Match)
	max?:     #Matcher @go(MaxMatch)
	mean?:    #Matcher @go(MeanMatch)

	// Origin is populated at collection time (candy:<name>/box:<name>/deploy-*),
	// NEVER authored (Go yaml:"-"), but it TRAVELS in the ai.opencharly.description
	// OCI-label JSON (Go json:"origin") — so the generated spec.Op MUST carry it
	// for a faithful drop-in (do NOT @go(-) it). gengotypes emits
	// `json:"origin,omitempty"`, matching the hand tag.
	origin?: string @go(Origin)

	// venue + intent_do are RUNTIME-DERIVED, never authored: venue is stamped
	// from a step's bundle-tree position (flattenBundleVenues), intent_do from
	// the enclosing Step's keyword (run→act / check→assert / agent→instruct).
	// They are generated onto the Go struct (the check runner persists them on
	// the Op-by-value passed to runOne / EffectiveDo), but the #Step authoring
	// arms forbid them (`venue?: _|_, intent_do?: _|_`) so authoring either is a
	// closed-schema rejection — exactly the contract the retired `pod:`/`on:`
	// fields enforced. yaml:"-" (never read from YAML); json omits them when
	// empty (the bake-time state, so they never leak into the description label).
	venue?:     string @go(Venue)
	intent_do?: string @go(IntentDo)

	// --- file/copy/write SHARED modifier ---
	// `mode` is the SHARED octal-permission modifier: the copy/write install verbs read
	// Op.Mode at deploy (the external local/vm deploy walk via kit.ParseTaskMode),
	// so it STAYS in #Op. The file-EXCLUSIVE fields (file/exists/owner/group_of/filetype/
	// contains/sha256) LEFT #Op — they are read ONLY by the `file` plugin verb and now live
	// in its #FileInput (candy/plugin-file, with the contains-default semantic
	// reproduced via the candy's decodeContainsList). The state-provision migrator MOVES `mode` into a
	// file step's plugin_input while LEAVING it here for copy/write (the shared-companion
	// pattern, like gid between unix_group and user).
	mode?: string & =~"^0[0-7]{3,4}$"

	// exclude_distro — a SHARED step-level skip filter read by the generic runOne for
	// EVERY verb (skip the step when any image distro tag intersects the list), NOT a
	// package-exclusive field, so it STAYS on #Op. The `package`-exclusive fields
	// (installed/version/package_map) MOVED into #PackageInput when `package` extracted.
	exclude_distro?: [...string] @go(ExcludeDistros)

	// `package`/`installed`/`version`/`package_map` are NOT here — `package` is an
	// extracted plugin verb (plugin: package + #PackageInput, candy/plugin-package).
	// It left #OpVerb/spec.OpVerbs, and installed/version/package_map (read ONLY by the
	// package verb off the step Op) MOVED into #PackageInput with it. The shared
	// `exclude_distro` modifier above is NOT package-exclusive and stays on #Op.
	//
	// `service`/`running`/`enabled` are NOT here — `service` is an extracted plugin
	// verb (plugin: service + #ServiceInput, candy/plugin-service). It left
	// #OpVerb/spec.OpVerbs, and `running`/`enabled` (read ONLY by the service verb off
	// the step Op) MOVED into #ServiceInput with it. `running` was reproduced standalone
	// in #ProcessInput when `process` extracted (process reads its own plugin_input, not
	// #Op), so removing it here leaves process untouched.

	// --- command-verb matchers (SHARED via matchAll: the `command` plugin verb +
	// the 11 live-container verbs assert exit_status/stdout/stderr off the step Op,
	// so they STAY in #Op; only the command-EXCLUSIVE command/in_container/background/
	// from_host moved into #CommandInput) ---
	exit_status?: int @go(ExitStatus,type=*int)
	stdout?:      #MatcherList
	stderr?:      #MatcherList

	// --- shared request modifiers (the http plugin verb + the live cdp/dbus/libvirt
	// verbs read these off the step Op; they are NOT carried in the http plugin's
	// plugin_input — the http-exclusive request fields status/body/header/…/ca_file
	// moved into #HttpInput, see candy/plugin-http) ---
	method?:       string
	request_body?: string @go(RequestBody)

	// --- cdp / wl / dbus / vnc / spice modifiers ---
	tab?:        string
	expression?: string
	url?:        string @go(URL)
	selector?:   string
	dest?:       string
	path?:       string
	arg?: [...string] @go(Args)
	artifact?:                 string
	artifact_min_bytes?:       int & >=0                    @go(ArtifactMinBytes,type=int)
	artifact_min_dimensions?:  string & =~"^[0-9]+x[0-9]+$" @go(ArtifactMinDimensions)
	artifact_not_uniform?:     bool                         @go(ArtifactNotUniform)
	artifact_min_cast_events?: int & >=0                    @go(ArtifactMinCastEvents,type=int)
	x?:                        int                          @go(,type=int)
	y?:                        int                          @go(,type=int)
	x2?:                       int                          @go(,type=int)
	y2?:                       int                          @go(,type=int)
	button?:                   string
	text?:                     string
	key?:                      string @go(KeyName)
	combo?:                    string
	direction?:                string
	amount?:                   int @go(,type=int)
	target?:                   string
	action?:                   string
	query?:                    string

	// --- mcp ---
	mcp_name?: string @go(McpName)
	tool?:     string
	uri?:      string @go(URI)
	input?:    string

	// --- adb / appium ---
	apk?:         string
	property?:    string
	caps?:        string
	strategy?:    string
	session?:     string
	app_id?:      string @go(AppId)
	source?:      string
	arch?:        string
	app_version?: string @go(AppVersion)
	activity?:    string
	attribute?:   string
	percent?:     string
	keycode?:     int @go(,type=int)
	params?:      string

	// --- record ---
	record_name?:  string    @go(RecordName)
	record_mode?:  string    @go(RecordMode)
	record_fps?:   int & >=0 @go(RecordFps,type=int)
	record_audio?: bool      @go(RecordAudio)
}

// Live-container verb method allowlists — the per-verb method-name enums on core
// #Op. A method outside the set is rejected declaratively in CUE; the per-verb
// method contract + required-modifier checks live in each verb's out-of-process plugin.
#CdpMethod:     ("status" | "list" | "url" | "text" | "html" | "eval" | "axtree" | "coords" | "raw" | "wait" | "screenshot" | "open" | "close" | "click" | "type" | "spa-status" | "spa-click" | "spa-type" | "spa-key" | "spa-key-combo" | "spa-mouse") @go(-)
#WlMethod:      "status" | "toplevel" | "windows" | "geometry" | "xprop" | "atspi" | "screenshot" | "clipboard" | "click" | "double-click" | "mouse" | "scroll" | "drag" | "type" | "key" | "key-combo" | "focus" | "close" | "fullscreen" | "minimize" | "exec" | "resolution" | "overlay-list" | "overlay-status" | "overlay-show" | "overlay-hide" | "sway-tree" | "sway-workspaces" | "sway-outputs" | "sway-msg" | "sway-focus" | "sway-move" | "sway-resize" | "sway-layout" | "sway-workspace" | "sway-kill" | "sway-floating" | "sway-reload" @go(-)
#DbusMethod:    "list" | "call" | "introspect" | "notify" @go(-)
#VncMethod:     "status" | "screenshot" | "click" | "mouse" | "type" | "key" | "rfb" @go(-)
#McpMethod:     "ping" | "servers" | "list-tools" | "list-resources" | "list-prompts" | "call" | "read" @go(-)
#RecordMethod:  "list" | "start" | "stop" | "cmd" @go(-)
#SpiceMethod:   "status" | "screenshot" | "cursor" | "click" | "mouse" | "type" | "key" @go(-)
#LibvirtMethod: "list" | "info" | "screenshot" | "send-key" | "passwd" | "qmp" | "domain-xml" | "console" | "events" | "guest/ping" | "guest/info" | "guest/os-info" | "guest/time" | "guest/hostname" | "guest/users" | "guest/interfaces" | "guest/disks" | "guest/fsinfo" | "guest/vcpus" | "guest/exec" | "guest/fstrim" | "snapshot/list" | "snapshot/create" | "snapshot/info" | "snapshot/revert" | "snapshot/delete" @go(-)
#KubeMethod:    "nodes" | "wait-nodes" | "pods" | "wait-ready" | "ingress" | "ingressclass" | "storageclass" | "service" | "lb-external-ip" | "addons" | "apply" | "delete" | "raw" @go(-)
#AdbMethod:     "devices" | "shell" | "install" | "install-app" | "uninstall" | "getprop" | "screencap" | "logcat-tail" | "wait-for-device" | "wait-ui-settled" | "current-focus" | "keyevent" @go(-)
#AppiumMethod:  "status" | "session-create" | "session-delete" | "install-app" | "find" | "click" | "send-keys" | "screenshot" | "get-text" | "get-attribute" | "clear" | "find-all" | "source" | "back" | "gesture-tap" | "gesture-double-tap" | "gesture-long-press" | "gesture-drag" | "gesture-swipe" | "gesture-scroll" | "gesture-fling" | "gesture-pinch-open" | "gesture-pinch-close" | "app-start-activity" | "app-activate" | "app-terminate" | "app-remove" | "app-clear" | "app-is-installed" | "app-state" | "app-current-activity" | "app-current-package" | "key-press" | "key-hide" | "key-shown" | "device-info" | "device-battery" | "device-time" | "device-orientation" | "device-set-orientation" | "device-notifications" | "device-get-clipboard" | "device-set-clipboard" | "device-contexts" | "device-context" | "execute" | "raw" @go(-)

// Matcher operators (validMatcherOps, validate_check.go). A matcher is a scalar
// (implicit equals), or a single-operator map; #MatcherList accepts a single
// matcher OR a list. (The base no longer carries a contains-default list def — that
// shape left with the `file` verb's `contains` field and is now reproduced standalone
// in the file plugin's #FileContains, decoded with the substring default via
// decodeContainsList; no base #Op field uses it anymore.)
#MatchOpMap: {equals: _} | {not_equals: _} | {contains: _} | {not_contains: _} | {matches: _} | {not_matches: _} | {lt: _} | {le: _} | {gt: _} | {ge: _}

#Matcher: (string | bool | number | #MatchOpMap) @go(-) // gengotypes: hand Matcher (spec/union_types.go, ported from checkspec.go)

#MatcherList: (#Matcher | [...#Matcher]) @go(-) // gengotypes: hand MatcherList

// ---------------------------------------------------------------------------
// Build-vocabulary shared shapes (distro formats + builders).
// ---------------------------------------------------------------------------

// A BuildKit cache mount. dst is the absolute in-builder cache path; sharing is
// the BuildKit sharing mode; owned renders a uid/gid-owned cache.
#CacheMount: {
	dst:      string & =~"^/"
	sharing?: *"locked" | "shared" | "private"
	owned?:   bool
}

// Three-phase template set: prepare → install → cleanup, each with a container
// (Containerfile) and host (shell) rendering. Template bodies are Go
// text/template — strings, never parsed here.
#PhaseSet: {
	prepare?: #PhaseTemplates @go(Prepare,optional=nillable)
	install?: #PhaseTemplates @go(Install,optional=nillable)
	cleanup?: #PhaseTemplates @go(Cleanup,optional=nillable)
}

#PhaseTemplates: {
	container?: string
	host?:      string
}

// ---------------------------------------------------------------------------
// Cross-kind scalar/ref patterns (R3: one home, referenced everywhere).
// ---------------------------------------------------------------------------

// CalVer schema/entity version: YYYY.DDD.HHMM.
#CalVer: string & =~"^[0-9]{4}\\.[0-9]{1,3}\\.[0-9]{3,4}$" @go(-)

// A lowercase-hyphenated entity name / cross-ref.
#EntityRef: string & =~"^[a-z0-9]+(-[a-z0-9]+)*$" @go(-)

// A candy ref: bare lowercase-hyphenated name OR a remote @github…:vTAG ref.
#CandyRef: string & =~"^(@.+|[a-z0-9]+(-[a-z0-9]+)*)$" @go(-)

// A host:container port pin (optionally IP-prefixed).
#PortPin: string & =~"^(\\[[0-9a-fA-F:]+\\]:|[0-9]{1,3}(\\.[0-9]{1,3}){3}:)?[0-9]+:[0-9]+$" @go(-)

// A memory/disk size, e.g. "2G", "8192M", "6.5Gi".
#VmSize: string & =~"^[0-9]+(\\.[0-9]+)?([KMGTP]i?B?)?$" @go(-)

// A container-resource size (SecurityConfig shm_size/memory_*).
#Size: string & =~"^[0-9]+(\\.[0-9]+)?[kKmMgG]?$" @go(-)

// An env entry: KEY=VALUE (value may be empty / contain =).
#EnvVar: string & =~"^[A-Za-z_][A-Za-z0-9_]*=.*$" @go(-)

// A YAML scalar Go decodes into a string (map[string]string / string field):
// yaml.v3 coerces an unquoted int/bool/float to its literal text, so an
// idiomatic `PORT: 8080` is a valid string value. #StrMap is the matching map.
#StrVal: string | number | bool // gengotypes: emits `type StrVal any` — referenced by inline {[string]: #StrVal} maps

#StrMap: ({[string]: #StrVal}) @go(-) // gengotypes: hand StrMap = map[string]string (spec/union_types.go)

// Go time.ParseDuration string (units ns/us/µs/ms/s/m/h), e.g. "30m", "1h30m".
#Duration: string & =~"^[0-9]+(\\.[0-9]+)?(ns|us|µs|ms|s|m|h)([0-9]+(\\.[0-9]+)?(ns|us|µs|ms|s|m|h))*$" @go(-)

// ---------------------------------------------------------------------------
// Container security (SecurityConfig) — shared by box / candy / deploy / sidecar.
// ---------------------------------------------------------------------------
#Security: {
	privileged?: bool
	cgroupns?:   "host" | "private" | "" @go(CgroupNS)
	cap_add?: [...string] @go(CapAdd)
	devices?: [...string]
	security_opt?: [...string] @go(SecurityOpt)
	ipc_mode?: "host" | "private" | "shareable" | "" @go(IpcMode)
	shm_size?: #Size                                 @go(ShmSize)
	group_add?: [...string] @go(GroupAdd)
	mount?: [...string] @go(Mounts)
	memory_max?:      #Size @go(MemoryMax)
	memory_high?:     #Size @go(MemoryHigh)
	memory_swap_max?: #Size @go(MemorySwapMax)
	cpus?:            string & =~"^[0-9]+(\\.[0-9]+)?$"
}

// ---------------------------------------------------------------------------
// Shell-rc config (ShellConfig/ShellSpec) — shared by box + candy. CLOSED: the
// Go UnmarshalYAML rejects any key outside the 4 intrinsic + 4 shell names.
// ---------------------------------------------------------------------------
#Shell: {
	init?: string
	path_append?: [...string] @go(PathAppend)
	path?:     string
	priority?: int @go(,type=int)
	bash?:     #ShellSpec @go(Bash,optional=nillable)
	zsh?:      #ShellSpec @go(Zsh,optional=nillable)
	fish?:     #ShellSpec @go(Fish,optional=nillable)
	sh?:       #ShellSpec @go(Sh,optional=nillable)
}
#ShellSpec: {
	init?: string
	path_append?: [...string] @go(PathAppend)
	path?: string
}

// ---------------------------------------------------------------------------
// Calamares package surface (PackageItem/DistroPackages/AURPackages) — shared by
// candy + group. `repo` is the genuine typed-open passthrough (#RepoBlock).
// ---------------------------------------------------------------------------
// bare scalar shorthand XOR object form.
#PackageItem: ((string & !="") | {
		name:         string & !=""
		description?: string & !=""
}) @go(-) // gengotypes: hand PackageItem (spec/union_types.go)
#DistroPackages: {
	package?: [...#PackageItem]
	copr?: [...(string & !="")]
	repo?: [...#RepoBlock]
	exclude?: [...(string & !="")]
	option?: [...(string & !="")] @go(Options)
	module?: [...(string & !="")]
	aur?: #AUR @go(AUR,optional=nillable)
}
#AUR: {
	package?: [...#PackageItem]
	option?: [...(string & !="")] @go(Options)
	replace?: [...(string & !="")] @go(Replaces)
}

// Free-form per-distro upstream-repo block: `name` is load-bearing (the only
// key any code requires), the rest pass through verbatim to install templates.
#RepoBlock: {
	name: string & !=""
	...
} @go(-) // gengotypes: hand-written `type RepoBlock = map[string]any` (spec/scalar_aliases.go) — an ALIAS so []RepoBlock IS []map[string]any (toMapSlice / template rendering)

// install_opts gates (deploy.go InstallOptsConfig) — shared by local + deploy.
#InstallOpts: {
	with_service?:       bool @go(WithServices)
	allow_repo_changes?: bool @go(AllowRepoChanges)
	allow_root_tasks?:   bool @go(AllowRootTasks)
	skip_incompatible?:  bool @go(SkipIncompatible)
	verify?:             bool
	builder_image?:      string & !="" @go(BuilderImage)
}
