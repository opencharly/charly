// Authoritative CUE source for Charly's gRPC transport contract.
// proto/plugin.proto and its language bindings are generated artifacts.
package protocol

protocol: {
	"syntax":     "proto3"
	"package":    "charlyplugin"
	"go_package": "github.com/opencharly/spec/proto"
	"doc": """
		The Charly plugin gRPC contract. One Provider service mirrors the unified
		provider interface; typed protobuf messages carry transport, streaming,
		and handshake control while CUE-generated JSON payloads carry domain data.
		"""
	"messages": [
		{
			"name": "Empty"
		},
		{
			"name": "Capabilities"
			"fields": [
				{
					"name":   "calver"
					"type":   "string"
					"number": 1
					"doc":    "the plugin's CalVer (CalVer is the version authority)"
				},
				{
					"name":   "protocol_version"
					"type":   "uint32"
					"number": 2
					"doc":    "thin secondary gate; never duplicates CalVer"
				},
				{
					"name":     "provided"
					"type":     "ProvidedCapability"
					"number":   3
					"doc":      "the unit's capabilities + the def validating each input"
					"repeated": true
				},
				{
					"name":   "schema_cue"
					"type":   "string"
					"number": 4
					"doc":    "the unit's package-less, SELF-CONTAINED .cue source text"
				},
			]
		},
		{
			"name": "ProvidedCapability"
			"doc":  """
				ProvidedCapability — one served capability plus the CUE def that validates its
				plugin_input. The schema travels with the plugin over Describe (the same channel
				for in-proc builtin and out-of-proc external — zero distinction), so the host
				validates authored plugin_input against `base ++ served` without ever reading a
				candy's schema/ dir. `input_def` is explicit (no Title-case naming convention).
				"""
			"fields": [
				{
					"name":   "class"
					"type":   "string"
					"number": 1
					"doc":    "\"verb\" / \"kind\" / \"deploy\" / \"step\" / \"builder\""
				},
				{
					"name":   "word"
					"type":   "string"
					"number": 2
					"doc":    "the reserved word, e.g. \"externalprobe\""
				},
				{
					"name":   "input_def"
					"type":   "string"
					"number": 3
					"doc":    "the CUE def for this word's plugin_input, e.g. \"#ExternalprobeInput\""
				},
				{
					"name":   "step_contract"
					"type":   "StepContract"
					"number": 4
					"doc":    "set ONLY for class=\"step\" (F3): the plugin-declared install-step contract"
				},
				{
					"name":   "structural"
					"type":   "bool"
					"number": 5
					"doc":    "set ONLY for class=\"kind\" (F5): the kind decodes a STRUCTURAL entity (a spec.Deploy member tree -\u003e uf.Fleet) rather than a FLAT body (-\u003e uf.PluginKinds)"
				},
				{
					"name":   "lifecycle"
					"type":   "bool"
					"number": 6
					"doc":    "set ONLY for class=\"deploy\" (F6): the substrate brings its OWN host-side venue lifecycle (PrepareVenue/Start/Stop/...) served over the lifecycle Ops; the host registers a wire-backed substrateLifecycle for it"
				},
				{
					"name":   "preresolve"
					"type":   "bool"
					"number": 7
					"doc":    "set ONLY for class=\"deploy\" (F6): the substrate declares a host-side PRERESOLVE step (OpPreresolve) the host runs before apply, shipping the opaque result in DeployVenue.Substrate"
				},
				{
					"name":   "validates"
					"type":   "bool"
					"number": 8
					"doc":    "set ONLY for class=\"kind\" (F7/C8): the kind serves a deep OpValidate check (returns Diagnostics) BEYOND the static CUE input-def gate; the host dispatches OpValidate at load"
				},
				{
					"name":   "phase"
					"type":   "string"
					"number": 9
					"doc":    "F9: the plugin lifecycle PHASE (sdk.Phase*; \"\" =\u003e runtime default) — the ordered point at which the kernel loads/invokes the plugin; \"bootstrap\" runs BEFORE config validation/migration"
				},
				{
					"name":   "primary"
					"type":   "string"
					"number": 10
					"doc":    "set ONLY for class=\"verb\": the input field the scalar sugar shorthand targets (`file: /x` -\u003e plugin_input: {\u003cprimary\u003e: \"/x\"}); \"\" =\u003e map input only"
				},
				{
					"name":   "deploy_traits"
					"type":   "DeployTraits"
					"number": 11
					"doc":    "set ONLY for class=\"kind\" (P9): a SUBSTRATE kind's DECLARED deploy behaviour traits, the SINGLE plugin-declared source kit.StampDescent stamps onto node.Descent — the consult sites read the traits off node.Descent instead of switching on the substrate kind word"
				},
				{
					"name":     "subcommands"
					"type":     "CLISubcommand"
					"number":   12
					"doc":      "set ONLY for class=\"command\": the plugin's DECLARED one-level-deep CLI subcommand catalog (name+help). Lets the host build a REAL nested Kong grammar (in place of the opaque `[<args>...]` pass-through holder every command-class capability otherwise gets) and synthesize a dotted \"<word>.<name>\" CLI-model leaf per entry for `charly __cli-model` / MCP tool generation. Empty (the default) preserves today's flat pass-through behavior byte-for-byte."
					"repeated": true
				},
				{
					"name":   "command_model_json"
					"type":   "bytes"
					"number": 13
					"doc":    "CUE #CLIModel JSON for class=command; lets CLI and MCP reflect plugin-owned leaves without importing plugin code"
				},
			]
		},
		{
			"name": "CLISubcommand"
			"doc":  """
				CLISubcommand — one DECLARED child of a class="command" capability's own CLI word (F-CLI-NEST).
				A plain name+help pair, not a full grammar: the host renders it as a Kong `cmd:""` child whose
				OWN body is still a pass-through Args leaf (the plugin's real internal flag/positional shape
				stays invisible to the host, exactly like today's flat holder — only the NAMING becomes real).
				A hidden child (hidden=true) is HIDDEN-BUT-REACHABLE: still a real Kong `cmd:""` node tagged
				`hidden:""` (so it DISPATCHES — e.g. the iterate harness's `charly check run-local` re-exec —
				but stays out of `--help` and the CLI model/MCP tool surface).
				"""
			"fields": [
				{
					"name":   "name"
					"type":   "string"
					"number": 1
					"doc":    "the subcommand word, e.g. \"live\", \"boxes\""
				},
				{
					"name":   "help"
					"type":   "string"
					"number": 2
					"doc":    "one-line help text, shown in `--help` and used as the MCP tool description"
				},
				{
					"name":   "hidden"
					"type":   "bool"
					"number": 3
					"doc":    "hidden-but-reachable: the host renders a hidden child with a Kong `hidden:\"\"` tag — it is still a REAL dispatchable cmd node, but `--help` and the CLI model (MCP) keep it invisible, mirroring the `hidden:\"\"` tag on the plugin's own grammar struct field (KongSubcommands derives it)"
				},
			]
		},
		{
			"name": "DeployTraits"
			"doc":  """
				DeployTraits — a SUBSTRATE kind's DECLARED deploy behaviour (P9), advertised per substrate
				word over Describe and stamped by kit.StampDescent onto the node's DescentDescriptor. This is
				the SINGLE plugin-declared source for "how does this substrate behave in the deploy chain",
				so the kernel consults it BY TRAIT (off node.Descent) — never by switching on the kind word.
				Canonical table: pod=container+image_backed+image_context; vm=ssh+machine_venue+exclusive_venue;
				local=shell+machine_venue; kubernetes=shell+image_context+leaf_only; android=parent; zero value =
				external-in-place. Empty/absent for every non-kind (or non-substrate kind) capability.
				"""
			"fields": [
				{
					"name":   "venue"
					"type":   "string"
					"number": 1
					"doc":    "\"container\" | \"ssh\" | \"shell\" | \"parent\" | \"none\": how commands physically execute in this substrate's venue"
				},
				{
					"name":   "image_backed"
					"type":   "bool"
					"number": 2
					"doc":    "the substrate runs a baked OCI image (pod)"
				},
				{
					"name":   "image_context"
					"type":   "bool"
					"number": 3
					"doc":    "the substrate composes over an image build context (pod overlay, kubernetes manifests)"
				},
				{
					"name":   "machine_venue"
					"type":   "bool"
					"number": 4
					"doc":    "the substrate is a full machine with a system init (host/vm/local) — services render as systemd units, not a container init"
				},
				{
					"name":   "exclusive_venue"
					"type":   "bool"
					"number": 5
					"doc":    "the substrate holds an exclusive host-resource lease boundary (vm)"
				},
				{
					"name":   "leaf_only"
					"type":   "bool"
					"number": 6
					"doc":    "the substrate is a deploy-chain LEAF — it cannot be descended into (kubernetes)"
				},
				{
					"name":   "bracketed_lifecycle"
					"type":   "bool"
					"number": 7
					"doc":    "the substrate's Start/Stop accept direct-mode CLI opts AND need the Q1 resource-arbiter claim bracketed around the dispatch (pod); a substrate that manages its own venue lifecycle + resource claim (vm) leaves this false"
				},
				{
					"name":   "bed_target"
					"type":   "bool"
					"number": 8
					"doc":    "the substrate is a valid kind:check bed target (pod/vm/local/android); kubernetes and unknown words are not"
				},
				{
					"name":   "supports_ephemeral"
					"type":   "bool"
					"number": 9
					"doc":    "the substrate's Add/Del path wires the ephemeral register/teardown seam (TTL timer + charly.yml persistence) — vm only today"
				},
				{
					"name":   "supports_from_snapshot"
					"type":   "bool"
					"number": 10
					"doc":    "the substrate has a backing chain from_snapshot restores from — vm only"
				},
			]
		},
		{
			"name": "StepContract"
			"doc":  """
				StepContract — a class="step" plugin's DECLARED install-step contract (F3): where the
				step's effect lands (scope) + where its commands execute (venue) + the opt-in gate it
				needs. This is what makes an external step kind a first-class IR step whose privilege /
				gating the host applies WITHOUT a compiled-in case (the open default arm). Reverse is NOT
				declared — an external step's teardown ops are DYNAMIC, recorded from its OpExecute reply
				(the same record-and-replay as ExternalPluginStep). Empty/absent for non-step capabilities.
				"""
			"fields": [
				{
					"name":   "scope"
					"type":   "string"
					"number": 1
					"doc":    "\"system\" | \"user\" | \"user-profile\""
				},
				{
					"name":   "venue"
					"type":   "int32"
					"number": 2
					"doc":    "Venue enum: 0=host-native, 1=container-builder, 2=skip"
				},
				{
					"name":   "gate"
					"type":   "string"
					"number": 3
					"doc":    "\"\" | \"allow-repo-changes\" | \"allow-root-tasks\" | \"with-services\""
				},
				{
					"name":   "emits"
					"type":   "bool"
					"number": 4
					"doc":    "F-STEP-EMIT: the step produces a build-context Containerfile FRAGMENT (Invoke(OpEmit)); the pod-overlay OCITarget bakes it. false =\u003e deploy-only step (no build fragment; OCITarget skips it, like apk on an image build)"
				},
			]
		},
		{
			"name": "InvokeRequest"
			"fields": [
				{
					"name":   "reserved"
					"type":   "string"
					"number": 1
					"doc":    "the reserved word, e.g. \"exampleprobe\",\"cdp\",\"local\""
				},
				{
					"name":   "op"
					"type":   "string"
					"number": 2
					"doc":    "operation selector for the word's class"
				},
				{
					"name":   "params_json"
					"type":   "bytes"
					"number": 3
					"doc":    "the CUE-generated params (Op for verbs/steps; entity for kinds)"
				},
				{
					"name":   "env_json"
					"type":   "bytes"
					"number": 4
					"doc":    "snapshotCheckEnv / venue descriptor (serializable invocation ctx)"
				},
				{
					"name":   "class"
					"type":   "string"
					"number": 5
					"doc":    "the ProviderClass (\"verb\"/\"kind\"/...) — words aren't unique across classes"
				},
				{
					"name":   "executor_broker_id"
					"type":   "uint32"
					"number": 6
					"doc":    "E3b: the go-plugin broker id the host serves ExecutorService on for a deploy/step/builder op; 0 = none (verb/kind ops need no executor)"
				},
			]
		},
		{
			"name": "InvokeReply"
			"doc":  "CheckResult / InstallPlan / Diagnostics, JSON"
			"fields": [
				{
					"name":   "result_json"
					"type":   "bytes"
					"number": 1
				},
			]
		},
		{
			"name": "Frame"
			"doc":  "one streamed result frame (single-shot sends one)"
			"fields": [
				{
					"name":   "result_json"
					"type":   "bytes"
					"number": 1
				},
			]
		},
		{
			"name": "ChannelFrame"
			"doc":  "ChannelFrame is the generic, bidirectional provider process channel. Domain payloads remain CUE-generated JSON; this envelope owns ordering, flow control, terminal data, and lifecycle signals without naming a runtime or transport."
			"fields": [
				{
					"name":   "request_id"
					"type":   "string"
					"number": 1
					"doc":    "stable idempotency/correlation key for the channel"
				},
				{
					"name":   "sequence"
					"type":   "uint64"
					"number": 2
					"doc":    "monotonic sender sequence; zero is reserved for an unsequenced open"
				},
				{
					"name":   "ack_sequence"
					"type":   "uint64"
					"number": 3
					"doc":    "highest contiguous peer sequence consumed"
				},
				{
					"name":   "kind"
					"type":   "string"
					"number": 4
					"doc":    "open|stdin|stdout|stderr|terminal|status|resize|signal|ack|cancel|exit|error|resync"
				},
				{
					"name":   "class"
					"type":   "string"
					"number": 5
					"doc":    "provider class, set on open"
				},
				{
					"name":   "reserved"
					"type":   "string"
					"number": 6
					"doc":    "provider word, set on open"
				},
				{
					"name":   "op"
					"type":   "string"
					"number": 7
					"doc":    "provider operation, set on open"
				},
				{
					"name":   "payload_json"
					"type":   "bytes"
					"number": 8
					"doc":    "CUE-generated domain payload or structured status"
				},
				{
					"name":   "data"
					"type":   "bytes"
					"number": 9
					"doc":    "binary-safe process or terminal bytes; never shell-interpreted"
				},
				{
					"name":   "name"
					"type":   "string"
					"number": 10
					"doc":    "canonical key, signal, stream, or status name"
				},
				{
					"name":   "cols"
					"type":   "uint32"
					"number": 11
				},
				{
					"name":   "rows"
					"type":   "uint32"
					"number": 12
				},
				{
					"name":   "exit_code"
					"type":   "int32"
					"number": 13
				},
				{
					"name":   "error"
					"type":   "string"
					"number": 14
				},
				{
					"name":   "target_json"
					"type":   "bytes"
					"number": 15
					"doc":    "CUE-generated generic target chain, set on open"
				},
				{
					"name":   "replay_from"
					"type":   "uint64"
					"number": 16
					"doc":    "first sequence requested during attach/resynchronization"
				},
			]
		},
		{
			"name": "InvokeProviderRequest"
			"doc":  """
				InvokeProviderRequest mirrors InvokeRequest minus the broker id (the host already holds the
				reverse context): dispatch op `op` on provider (class, reserved) with params/env (F10).
				"""
			"fields": [
				{
					"name":   "class"
					"type":   "string"
					"number": 1
				},
				{
					"name":   "reserved"
					"type":   "string"
					"number": 2
				},
				{
					"name":   "op"
					"type":   "string"
					"number": 3
				},
				{
					"name":   "params_json"
					"type":   "bytes"
					"number": 4
				},
				{
					"name":   "env_json"
					"type":   "bytes"
					"number": 5
				},
				{
					"name":   "venue_descriptor_json"
					"type":   "bytes"
					"number": 6
					"doc":    """
						OPTIONAL marshalled spec.VenueDescriptor (S1 — the venue-scoped-executor-session
						seam). The calling plugin supplies its OWN self-described venue (shell or ssh) when
						it wants the target provider Invoked WITH a live executor but holds no enclosing
						executor of its own to forward (e.g. a verb/kind Invoke with no deploy-context
						broker). On presence, the host re-materializes a FRESH DeployExecutor from the
						descriptor (venueFromDescriptor) and threads THAT onto the nested InvokeWithExecutor
						call instead of the caller's own executor. Empty/absent — byte-identical prior
						behavior (the caller's own executor, if any, is forwarded as before).
						"""
				},
				{
					"name":   "extra_ref"
					"type":   "string"
					"number": 7
					"doc":    """
						OPTIONAL canonical candy ref (S3b — the Pass-2 lazy-connect gap). The host's
						InvokeProvider handler falls back to connectPluginByWordRef(class, word, extraRef)
						on a registry miss (S2); passing "" only ever reaches Pass-1 (the calling project's
						own candy closure) — a target declared nowhere in that closure but resolvable via an
						explicit @github canonical ref (the same Pass-2 fetch the credential/vm/kube host
						adapters already use) needs this field set. Empty/absent — byte-identical S2
						behavior (Pass-1 only).
						"""
				},
			]
		},
		{
			"name": "HostBuildRequest"
			"doc":  """
				HostBuildRequest names a registered host-builder `kind` (e.g. "plugin-binary", and — added by
				M13/M14 — "kustomize"/"image") + an opaque `spec_json` it interprets (F10). HostBuildReply
				carries the builder's opaque result (e.g. an artifact path/handle JSON) or an error.
				"""
			"fields": [
				{
					"name":   "kind"
					"type":   "string"
					"number": 1
				},
				{
					"name":   "spec_json"
					"type":   "bytes"
					"number": 2
				},
			]
		},
		{
			"name": "DescribeProviderRequest"
			"doc":  """
				DescribeProviderRequest (K5-A item 2): ask the host for the CACHED capability
				metadata of another provider by (class, word) — no live Describe round-trip, no
				Invoke. This is what a plugin needs to make a routing DECISION about a peer
				provider (e.g. "does class:step word X declare Emits=true?") without holding its
				own copy of the provider registry — the same registry-consult gap InvokeProvider
				solves for DISPATCH, DescribeProvider solves for METADATA. class/word are DATA (the
				F11 uniform-API invariant): the RPC itself never names a plugin word.
				"""
			"fields": [
				{
					"name":   "class"
					"type":   "string"
					"number": 1
				},
				{
					"name":   "word"
					"type":   "string"
					"number": 2
				},
			]
		},
		{
			"name": "DescribeProviderReply"
			"doc":  """
				found=false means (class, word) resolves to no CONNECTED provider (mirrors
				InvokeProvider's own "provider not connected" failure mode, but as a query result
				rather than an RPC error — a plugin routing decision often wants to distinguish
				"not found" from a transport failure). step_contract is populated only when the
				resolved provider is class="step" AND declares one (F3) — absent/nil for every
				other class, exactly like ProvidedCapability.step_contract's own optionality. Kept
				narrowly scoped to StepContract (the ONE cached sub-shape a consumer needs today,
				oci_step_emit.go's relocation) rather than the whole ProvidedCapability — extend
				with additional cached fields only when a consumer actually needs them.
				"""
			"fields": [
				{
					"name":   "found"
					"type":   "bool"
					"number": 1
				},
				{
					"name":   "step_contract"
					"type":   "StepContract"
					"number": 2
				},
			]
		},
		{
			"name": "HostBuildReply"
			"fields": [
				{
					"name":   "result_json"
					"type":   "bytes"
					"number": 1
				},
				{
					"name":   "error"
					"type":   "string"
					"number": 2
				},
			]
		},
		{
			"name": "VenueReply"
			"fields": [
				{
					"name":   "venue"
					"type":   "string"
					"number": 1
				},
			]
		},
		{
			"name": "RunRequest"
			"doc":  "opts_json = EmitOpts, JSON"
			"fields": [
				{
					"name":   "script"
					"type":   "string"
					"number": 1
				},
				{
					"name":   "opts_json"
					"type":   "bytes"
					"number": 2
				},
			]
		},
		{
			"name": "RunReply"
			"doc":  "empty error = success"
			"fields": [
				{
					"name":   "error"
					"type":   "string"
					"number": 1
				},
			]
		},
		{
			"name": "PutFileRequest"
			"doc":  "content placed at path; mode = octal perms; opts_json = EmitOpts"
			"fields": [
				{
					"name":   "path"
					"type":   "string"
					"number": 1
				},
				{
					"name":   "content"
					"type":   "bytes"
					"number": 2
				},
				{
					"name":   "mode"
					"type":   "uint32"
					"number": 3
				},
				{
					"name":   "owner_root"
					"type":   "bool"
					"number": 4
				},
				{
					"name":   "opts_json"
					"type":   "bytes"
					"number": 5
				},
			]
		},
		{
			"name": "PutFileReply"
			"doc":  "empty error = success"
			"fields": [
				{
					"name":   "error"
					"type":   "string"
					"number": 1
				},
			]
		},
		{
			"name": "CaptureReply"
			"doc":  "error = execution failure, NOT a non-zero exit"
			"fields": [
				{
					"name":   "stdout"
					"type":   "string"
					"number": 1
				},
				{
					"name":   "stderr"
					"type":   "string"
					"number": 2
				},
				{
					"name":   "exit_code"
					"type":   "int32"
					"number": 3
				},
				{
					"name":   "error"
					"type":   "string"
					"number": 4
				},
			]
		},
		{
			"name": "LiveReply"
			"doc":  "F12 RunInteractive/RunStream: stdout/stderr/stdin went LIVE to the operator's terminal (host-held), so no buffers — only the session's exit code; error = execution/spawn failure, NOT a non-zero exit (CaptureReply's split, sans buffers)"
			"fields": [
				{
					"name":   "exit_code"
					"type":   "int32"
					"number": 1
				},
				{
					"name":   "error"
					"type":   "string"
					"number": 2
				},
			]
		},
		{
			"name": "GetFileRequest"
			"fields": [
				{
					"name":   "path"
					"type":   "string"
					"number": 1
				},
				{
					"name":   "as_root"
					"type":   "bool"
					"number": 2
				},
				{
					"name":   "opts_json"
					"type":   "bytes"
					"number": 3
				},
			]
		},
		{
			"name": "GetFileReply"
			"fields": [
				{
					"name":   "content"
					"type":   "bytes"
					"number": 1
				},
				{
					"name":   "error"
					"type":   "string"
					"number": 2
				},
			]
		},
		{
			"name": "HostStepRequest"
			"doc":  """
				step_json = ONE spec.InstallStepView (a HOST-ENGINE step kind — Builder, LocalPkgInstall,
				SystemPackages, an act-verb Op, or ExternalPlugin — projected by stepToView); opts_json =
				EmitOpts. reverse_ops_json = the step's recorded []spec.ReverseOp (the plugin folds them
				into its DeployReply for record-and-replay teardown). error = a host-engine/apply FAILURE
				on the venue (the RPC itself succeeds — the failure rides the reply field, like
				RunReply/CaptureReply).
				"""
			"fields": [
				{
					"name":   "step_json"
					"type":   "bytes"
					"number": 1
				},
				{
					"name":   "opts_json"
					"type":   "bytes"
					"number": 2
				},
			]
		},
		{
			"name": "HostStepReply"
			"fields": [
				{
					"name":   "reverse_ops_json"
					"type":   "bytes"
					"number": 1
				},
				{
					"name":   "error"
					"type":   "string"
					"number": 2
				},
			]
		},
		{
			"name": "HTTPDoRequest"
			"doc":  """
				HTTPDoRequest carries the FULL request + per-request policy the host needs to build the
				client (httpClientFor) host-side: ca_pem is the resolved CA PEM bytes (the candy reads its
				authored ca_file host-side and ships the bytes), timeout is a Go duration string ("" = base).
				"""
			"fields": [
				{
					"name":   "method"
					"type":   "string"
					"number": 1
				},
				{
					"name":   "url"
					"type":   "string"
					"number": 2
				},
				{
					"name":   "body"
					"type":   "bytes"
					"number": 3
				},
				{
					"name":         "headers"
					"type":         "string"
					"number":       4
					"map_key_type": "string"
				},
				{
					"name":   "timeout"
					"type":   "string"
					"number": 5
				},
				{
					"name":   "allow_insecure"
					"type":   "bool"
					"number": 6
				},
				{
					"name":   "no_follow_redirects"
					"type":   "bool"
					"number": 7
				},
				{
					"name":   "ca_pem"
					"type":   "bytes"
					"number": 8
				},
			]
		},
		{
			"name": "HTTPDoReply"
			"doc":  """
				HTTPDoReply: status + body + response headers, or a transport-level error (the RPC itself
				succeeds — a failed request rides the error field, like RunReply/CaptureReply).
				"""
			"fields": [
				{
					"name":   "status"
					"type":   "int32"
					"number": 1
				},
				{
					"name":   "body"
					"type":   "bytes"
					"number": 2
				},
				{
					"name":   "header_blob"
					"type":   "string"
					"number": 3
					"doc":    "pre-formatted \"Key: value\\n\" blob (multi-value preserved), matcher-ready"
				},
				{
					"name":   "error"
					"type":   "string"
					"number": 4
				},
			]
		},
		{
			"name": "AddBackgroundRequest"
			"fields": [
				{
					"name":   "pid"
					"type":   "int32"
					"number": 1
				},
			]
		},
		{
			"name": "ResolveEndpointRequest"
			"doc":  "ResolveEndpointRequest: the in-venue TCP port to resolve to a host-reachable address."
			"fields": [
				{
					"name":   "port"
					"type":   "int32"
					"number": 1
				},
			]
		},
		{
			"name": "ResolveEndpointReply"
			"doc":  "ResolveEndpointReply: the host-reachable \"host:port\" addr, or a resolution error."
			"fields": [
				{
					"name":   "addr"
					"type":   "string"
					"number": 1
				},
				{
					"name":   "error"
					"type":   "string"
					"number": 2
				},
			]
		},
		{
			"name": "ResolveGraphicsEndpointRequest"
			"doc":  "ResolveGraphicsEndpointRequest: the graphics kind to resolve (\"vnc\" | \"spice\")."
			"fields": [
				{
					"name":   "kind"
					"type":   "string"
					"number": 1
				},
			]
		},
		{
			"name": "ResolveGraphicsEndpointReply"
			"doc":  """
				ResolveGraphicsEndpointReply: the dialable endpoint. Exactly one of addr / socket is set;
				password is the resolved ticket (empty = no auth). skip=true with skip_message signals a
				deployment with no graphics device of that kind (an N/A skip, not a failure).
				"""
			"fields": [
				{
					"name":   "addr"
					"type":   "string"
					"number": 1
				},
				{
					"name":   "socket"
					"type":   "string"
					"number": 2
				},
				{
					"name":   "password"
					"type":   "string"
					"number": 3
				},
				{
					"name":   "skip"
					"type":   "bool"
					"number": 4
				},
				{
					"name":   "skip_message"
					"type":   "string"
					"number": 5
				},
				{
					"name":   "error"
					"type":   "string"
					"number": 6
				},
			]
		},
		{
			"name": "ResolveImageLabelRequest"
			"doc":  "ResolveImageLabelRequest: the OCI label name to read (e.g. \"ai.opencharly.mcp_provide\")."
			"fields": [
				{
					"name":   "label"
					"type":   "string"
					"number": 1
				},
			]
		},
		{
			"name": "ResolveImageLabelReply"
			"doc":  "ResolveImageLabelReply: the raw label value (\"\" = label absent on the image)."
			"fields": [
				{
					"name":   "value"
					"type":   "string"
					"number": 1
				},
				{
					"name":   "error"
					"type":   "string"
					"number": 2
				},
			]
		},
	]
	"services": [
		{
			"name": "PluginMeta"
			"doc":  "PluginMeta — read once on connect."
			"methods": [
				{
					"name":     "Describe"
					"request":  "Empty"
					"response": "Capabilities"
				},
			]
		},
		{
			"name": "Provider"
			"doc":  """
				Provider — the ONE uniform capability service. `reserved` is the reserved word
				(the registry key); `op` selects the operation for that word's class
				(load/validate/run/emit/render/resolve); params_json + env_json carry the
				CUE-typed params + the serializable invocation context (snapshotCheckEnv).
				"""
			"methods": [
				{
					"name":     "Invoke"
					"request":  "InvokeRequest"
					"response": "InvokeReply"
					"doc":      "single-shot (the common path)"
				},
				{
					"name":             "InvokeStream"
					"request":          "InvokeRequest"
					"response":         "Frame"
					"doc":              "streaming (record/logcat/execute)"
					"server_streaming": true
				},
				{
					"name":             "Channel"
					"request":          "ChannelFrame"
					"response":         "ChannelFrame"
					"doc":              "generic bidirectional provider/process stream with explicit ordering, acknowledgements, cancellation, and resynchronization"
					"client_streaming": true
					"server_streaming": true
				},
			]
		},
		{
			"name": "ExecutorService"
			"doc":  """
				ExecutorService — the HOST-SERVED reverse channel (E3b). A provider's live
				DeployExecutor (shell/SSH on the venue) cannot cross the process boundary, so an
				OUT-OF-PROCESS provider drives it by calling BACK to the host: the host serves this
				service on the go-plugin GRPCBroker, passes its broker id in
				InvokeRequest.executor_broker_id, and the plugin dials that id to run ops against the
				real venue. Built-in providers use the typed DeployExecutor directly (no wire).
				RunSystem/RunUser/Venue are the deploy/step/builder legs (fire-a-script, error-only);
				PutFile is the deploy/step file-PLACEMENT leg — an out-of-process deploy/step plugin
				that EXECUTES an InstallPlan's steps pushes file content (units, env.d, the charly
				binary, builder artifacts) onto the venue, the kit.Executor.PutFile surface made
				wire-backed (the host materializes the carried bytes to a temp file, then delegates
				to the live DeployExecutor.PutFile). RunCapture/GetFile are the CHECK-VERB legs — an
				out-of-process exec-based check verb (record — and dbus/wl when they externalize)
				probes the live container by capturing stdout/stderr/exit or pulling a venue file (a
				screenshot/recording artifact), the kit.Executor.RunCapture/GetFile surface made
				wire-backed.

				RunHostStep is the HOST-ENGINE channel (the generalization of the former F3 build channel): the
				irreducible host machinery — the build ENGINE (Generator / OCITarget / ResolveBox /
				podman / BuilderRun / the injected EnsureImage closure / makepkg), the DistroConfig package-template
				render, the in-proc provider registry (ProvisionActor act verbs), and the broker that
				dispatches ANOTHER plugin — STAYS in charly's core (package main — it cannot move into
				the leaf plugin/kit package without dragging package main in). So an OUT-OF-PROCESS
				deploy/step plugin walking an InstallPlan that hits one of the HOST-ENGINE step kinds —
				BuilderStep (pixi/npm/cargo/aur), LocalPkgInstallStep (makepkg + pacman/dnf/apt),
				SystemPackagesStep (the format's host install template, rendered from the project
				DistroConfig), an act-verb OpStep (a `run: plugin: <verb>` whose builtin ProvisionActor
				shell needs the in-proc registry), or an ExternalPluginStep (a `run: plugin: <verb>`
				served by ANOTHER out-of-process plugin, dispatched over a NESTED reverse channel) —
				dials BACK to RunHostStep. The host reconstructs the step from its serializable view,
				runs the EXISTING in-core machinery on the host, applies the effect onto the venue via
				the EXISTING executor (the plugin's own venue, which the host already holds for this
				Invoke), and returns the step's recorded ReverseOps so the plugin folds them into its
				DeployReply (record-and-replay teardown). Every OTHER (plugin-renderable) step kind the
				plugin EXECUTES itself via the RunSystem/RunUser/PutFile legs — reaching RunHostStep
				with one of those is a plugin-walk bug. The plugin owns the plan WALK ordering; the host
				owns the host ENGINE.
				"""
			"methods": [
				{
					"name":     "Venue"
					"request":  "Empty"
					"response": "VenueReply"
				},
				{
					"name":     "RunSystem"
					"request":  "RunRequest"
					"response": "RunReply"
				},
				{
					"name":     "RunUser"
					"request":  "RunRequest"
					"response": "RunReply"
				},
				{
					"name":     "PutFile"
					"request":  "PutFileRequest"
					"response": "PutFileReply"
					"doc":      "place file content on the venue (binary-safe; owner_root → root:root via sudo)"
				},
				{
					"name":     "RunCapture"
					"request":  "RunRequest"
					"response": "CaptureReply"
					"doc":      "capture stdout/stderr/exit (no root escalation — callers add sudo)"
				},
				{
					"name":     "RunInteractive"
					"request":  "RunRequest"
					"response": "LiveReply"
					"doc":      "F12: run script on the venue wired to the operator's LIVE TTY (the host reverse-server runs IN the charly process, so it inherits os.Stdin/os.Stdout/os.Stderr = the operator's terminal — stdio NEVER crosses the wire, only script→exit; the child podman exec -it / ssh -t owns the PTY + resize + Ctrl-C). Blocks until the session ends; UNARY (host holds the TTY — the hostBuildCli doctrine). Consumers: charly shell (-it), charly cmd (-i)"
				},
				{
					"name":     "RunStream"
					"request":  "RunRequest"
					"response": "LiveReply"
					"doc":      "F12: run script on the venue streaming stdout/stderr LIVE to the operator (inherited os.Stdout/os.Stderr; no stdin). Blocks until exit. UNARY (host holds the terminal). Consumer: charly logs --follow"
				},
				{
					"name":     "GetFile"
					"request":  "GetFileRequest"
					"response": "GetFileReply"
					"doc":      "read a venue file (e.g. a record/screenshot artifact pulled back to the host)"
				},
				{
					"name":     "RunHostStep"
					"request":  "HostStepRequest"
					"response": "HostStepReply"
					"doc":      "run a HOST-ENGINE step (Builder/LocalPkgInstall/SystemPackages/act-OpStep/ExternalPlugin) on the host engine + apply onto the venue"
				},
				{
					"name":     "InvokeProvider"
					"request":  "InvokeProviderRequest"
					"response": "InvokeReply"
					"doc":      "F10 plugin↔plugin: the host resolves another provider by (class,word) + Invokes it on the calling plugin's behalf (threading the SAME venue executor) — the generalization of the RunHostStep ExternalPlugin arm to ANY class/op"
				},
				{
					"name":     "HostBuild"
					"request":  "HostBuildRequest"
					"response": "HostBuildReply"
					"doc":      "F10 host-build: the calling plugin requests a HOST-side build (the build engine stays in core) — the host runs the registered host-builder for `kind` and returns its result"
				},
				{
					"name":     "DescribeProvider"
					"request":  "DescribeProviderRequest"
					"response": "DescribeProviderReply"
					"doc":      "K5-A item 2: the host resolves another provider by (class,word) and returns its CACHED capability metadata (today: StepContract) — no Invoke, no live Describe round-trip. The metadata twin of InvokeProvider (dispatch) and HostBuild (host-side build)."
				},
			]
		},
		{
			"name": "CheckContextService"
			"doc":  """
				CheckContextService is the host-served reverse channel (F2) for the HOST-COUPLED
				check-verb kit (kit): the legs kit.CheckContext exposes that cannot ride
				the env_json snapshot because they hold a live host resource. It is served on the SAME
				go-plugin broker as ExecutorService (the broker id in InvokeRequest.executor_broker_id),
				so a kit verb served OUT-OF-PROCESS reaches BOTH the venue (Exec, via ExecutorService)
				AND these host-vantage legs. Two RPCs, both class-generic (any kit verb may use either —
				never a per-verb RPC, the Uniform API Invariant):
				- HTTPDo: the host issues an HTTP request from the CHARLY HOST's network namespace with
				the per-request TLS/redirect/CA policy applied (the http verb's cc.HTTPClient() leg —
				an *http.Client cannot cross the wire, so the REQUEST crosses and the host dials).
				- AddBackground: register a host-side background PID with the active plan run for teardown
				reap (the command verb's fire-and-forget leg).
				Mode/Box/Instance/Distros/DialTimeout are plain scalars and ride the env_json CheckEnv
				snapshot, NOT this service.
				"""
			"methods": [
				{
					"name":     "HTTPDo"
					"request":  "HTTPDoRequest"
					"response": "HTTPDoReply"
				},
				{
					"name":     "AddBackground"
					"request":  "AddBackgroundRequest"
					"response": "Empty"
				},
				{
					"name":     "ResolveEndpoint"
					"request":  "ResolveEndpointRequest"
					"response": "ResolveEndpointReply"
					"doc":      """
						ResolveEndpoint: resolve the check target's venue (container / VM / ssh / local) and
						return a host-reachable address for an in-venue TCP port — opening (and host-side
						tracking, for post-Invoke teardown) any ssh -L forward a VM/ssh venue needs. Class-
						generic: ANY endpoint check verb (cdp/vnc/spice/…) declares its port and dials the addr.
						"""
				},
				{
					"name":     "ResolveGraphicsEndpoint"
					"request":  "ResolveGraphicsEndpointRequest"
					"response": "ResolveGraphicsEndpointReply"
					"doc":      """
						ResolveGraphicsEndpoint: resolve a VM's <graphics type='<kind>'> listener (kind = "vnc"
						| "spice") to a dialable endpoint — the host owns the go-libvirt resolution, any
						qemu+ssh:// tunnel (tracked for post-Invoke teardown), the socket->TCP bridge a TCP-only
						client needs, and the credential-store password. Class-generic: parameterized by kind,
						shared by the vnc + spice verbs (never a per-verb RPC).
						"""
				},
				{
					"name":     "ResolveImageLabel"
					"request":  "ResolveImageLabelRequest"
					"response": "ResolveImageLabelReply"
					"doc":      """
						ResolveImageLabel: read one OCI label value off the deployment-under-test's image — the
						host owns the podman engine + container→image resolution the out-of-process plugin cannot
						reach. Class-generic (parameterized by label name): the mcp verb reads ai.opencharly.mcp_provide;
						any verb needing a baked label uses it. Empty value (label absent) is a valid reply.
						"""
				},
			]
		},
	]
}
