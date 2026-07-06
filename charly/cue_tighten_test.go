package main

// Proves the SCHEMA TIGHTENING has teeth: the closed structs, modeled libvirt/
// cloud_init/deploy subtrees, live-verb method enums, and scalar-coercion parity
// all behave as intended. Each REJECT case would PASS under the old loose
// schema (open `...` / blanket `{...}`) — so this file is the regression guard
// against re-loosening. Each ACCEPT case guards against over-tightening.

import (
	"strings"
	"testing"
)

// validateKindBody validates a YAML body AS an entity of `kind` (the body is the
// entity's own fields, e.g. a single value of the `vm:`/`deploy:` map — the
// built doc's root value IS the entity).
func validateKindBody(t *testing.T, kind, yamlBody string) error {
	t.Helper()
	doc, err := cueDocFromYAML("t.yml", []byte(yamlBody))
	if err != nil {
		return err
	}
	return validateEntityCUE(kind, "t.yml:"+kind, doc)
}

func TestCueTightening_RejectsAndAccepts(t *testing.T) {
	// candy cases go through the node-form manifest validator: the body is
	// wrapped as a compact node-form entity `x: {candy: <body>}` (the FULL body
	// inline in the kind value; plan steps author the plugin-verb sugar the
	// parse-time desugar rewrites — the authored plugin:/plugin_input: envelope
	// is a hard load error).
	candy := func(body string) string {
		var b strings.Builder
		b.WriteString("x:\n  candy:\n")
		for _, ln := range strings.Split(strings.TrimRight(body, "\n"), "\n") {
			if strings.TrimSpace(ln) == "" {
				continue
			}
			b.WriteString("  " + ln + "\n")
		}
		return b.String()
	}
	const candyHead = "  version: 2026.144.1443\n  name: x\n  description: d\n"
	const candyPlan = "  plan:\n  - check: c\n    file: /x\n"

	cases := []struct {
		name   string
		kind   string // "candy" → manifest validator; else entity-body validator
		yaml   string
		reject bool
	}{
		// --- closed #Candy rejects unknown top-level keys (replaces the Go typo guard) ---
		{"candy unknown top-level key", "candy",
			candy(candyHead + candyPlan + "  requir: [a]\n"), true},
		// --- the autostart/device latent-bug classes the tightening surfaced ---
		{"candy service autostart typo", "candy",
			candy(candyHead + candyPlan + "  service:\n  - name: s\n    exec: /bin/x\n    autostart: true\n"), true},
		{"candy service auto_start correct", "candy",
			candy(candyHead + candyPlan + "  service:\n  - name: s\n    exec: /bin/x\n    auto_start: true\n"), false},
		{"candy security device typo", "candy",
			candy(candyHead + candyPlan + "  security:\n    device: [/dev/fuse]\n"), true},
		{"candy security devices correct", "candy",
			candy(candyHead + candyPlan + "  security:\n    devices: [/dev/fuse]\n"), false},
		// --- scalar-coercion parity: an unquoted int/bool env value is a string ---
		{"candy env scalar int accepted", "candy",
			candy(candyHead + candyPlan + "  env:\n    PORT: 8080\n"), false},
		{"candy env scalar bool accepted", "candy",
			candy(candyHead + candyPlan + "  env:\n    DEBUG: true\n"), false},
		{"candy env PATH forbidden", "candy",
			candy(candyHead + candyPlan + "  env:\n    PATH: /x\n"), true},
		// --- live-verb method enum (cdp): eval is valid, the phantom check is gone ---
		{"candy cdp eval method accepted", "candy",
			candy(candyHead + "  plan:\n  - check: c\n    cdp:\n      method: eval\n      tab: \"1\"\n      expression: \"1+1\"\n"), false},
		// --- http is a builtin plugin verb authored via the `http:` sugar (the
		//     http-exclusive status/body/… ride the verb's input map, validated at
		//     runtime against the http plugin's spliced #HttpInput, not here) ---
		{"candy http plugin verb accepted", "candy",
			candy(candyHead + "  plan:\n  - check: c\n    http:\n      http: http://x/\n      status: 200\n    context: [runtime]\n"), false},

		// --- vm: the libvirt blanket {...} is now a CLOSED model ---
		{"vm libvirt unknown field rejected", "vm",
			"source:\n  kind: cloud_image\n  url: http://x\nlibvirt:\n  bogus_field: 1\n", true},
		{"vm libvirt modeled field accepted", "vm",
			"source:\n  kind: cloud_image\n  url: http://x\nlibvirt:\n  devices:\n    graphics:\n    - type: spice\n", false},
		{"vm cloud_init unknown field rejected", "vm",
			"source:\n  kind: cloud_image\n  url: http://x\ncloud_init:\n  bogus: 1\n", true},
		{"vm cloud_init extra passthrough accepted", "vm",
			"source:\n  kind: cloud_image\n  url: http://x\ncloud_init:\n  extra: |\n    #cloud-config\n", false},
		// --- vm source discriminated union: cross-arm field forbidden ---
		{"vm cloud_image with bootc field rejected", "vm",
			"source:\n  kind: cloud_image\n  url: http://x\n  box: y\n", true},

		// --- deploy: the sidecar/probes blankets are modeled; unknown key rejected ---
		{"deploy unknown key rejected", "deploy",
			"target: pod\nimage: x\nbogus_key: 1\n", true},
		{"deploy modeled fields accepted", "deploy",
			"target: pod\nimage: x\nrestart: always\nport: [\"8080:8080\"]\n", false},
		{"deploy bad restart enum rejected", "deploy",
			"target: pod\nimage: x\nrestart: sometimes\n", true},

		// --- box: closed; the retired port: field is forbidden ---
		{"box unknown key rejected", "box",
			"name: x\nbogus: 1\n", true},
		{"box retired port field rejected", "box",
			"name: x\nport: [\"8080:8080\"]\n", true},

		// --- candy apk: exactly-one package/apk + closed (the matchN→disjunction fix) ---
		{"candy apk package only accepted", "candy",
			candy(candyHead + candyPlan + "  apk:\n  - package: com.x\n"), false},
		{"candy apk both package and apk rejected", "candy",
			candy(candyHead + candyPlan + "  apk:\n  - package: com.x\n    apk: /tmp/x.apk\n"), true},
		{"candy apk neither rejected", "candy",
			candy(candyHead + candyPlan + "  apk:\n  - arch: x86_64\n"), true},
		{"candy apk unknown field rejected", "candy",
			candy(candyHead + candyPlan + "  apk:\n  - package: com.x\n    bogus: 1\n"), true},
		{"candy apk bad source rejected", "candy",
			candy(candyHead + candyPlan + "  apk:\n  - package: com.x\n    source: apk-mirror\n"), true},

		// --- vm ssh: port⊕port_auto mutex + closed (the matchN→disjunction fix) ---
		{"vm ssh port only accepted", "vm",
			"source:\n  kind: cloud_image\n  url: http://x\nssh:\n  port: 2222\n", false},
		{"vm ssh port_auto only accepted", "vm",
			"source:\n  kind: cloud_image\n  url: http://x\nssh:\n  port_auto: true\n", false},
		{"vm ssh port + port_auto rejected", "vm",
			"source:\n  kind: cloud_image\n  url: http://x\nssh:\n  port: 2222\n  port_auto: true\n", true},
		{"vm ssh unknown field rejected", "vm",
			"source:\n  kind: cloud_image\n  url: http://x\nssh:\n  bogus: 1\n", true},

		// --- cutover #9: candy port range (replaces deleted validatePort) ---
		{"candy port int accepted", "candy",
			candy(candyHead + candyPlan + "  port: [8080]\n"), false},
		{"candy port proto:port accepted", "candy",
			candy(candyHead + candyPlan + "  port: [\"tcp:5900\"]\n"), false},
		{"candy port out-of-range rejected", "candy",
			candy(candyHead + candyPlan + "  port: [99999]\n"), true},
		{"candy port zero rejected", "candy",
			candy(candyHead + candyPlan + "  port: [0]\n"), true},
		{"candy port_relay out-of-range rejected", "candy",
			candy(candyHead + candyPlan + "  port_relay: [99999]\n"), true},
		{"candy port_relay valid accepted", "candy",
			candy(candyHead + candyPlan + "  port_relay: [8080]\n"), false},
		// --- cutover #9: candy engine enum (replaces validateEngineConfig candy enum) ---
		{"candy engine bogus rejected", "candy",
			candy(candyHead + candyPlan + "  engine: bogus\n"), true},
		{"candy engine podman accepted", "candy",
			candy(candyHead + candyPlan + "  engine: podman\n"), false},
		// --- cutover #9: secret_require key format (replaces validateSecretDeps key check) ---
		{"candy secret key bad prefix rejected", "candy",
			candy(candyHead + candyPlan + "  secret_require:\n  - name: X\n    description: d\n    key: aws/access-key\n"), true},
		{"candy secret key valid accepted", "candy",
			candy(candyHead + candyPlan + "  secret_require:\n  - name: X\n    description: d\n    key: charly/api-key/openrouter\n"), false},
		// --- cutover #9: env/secret/mcp dep name+description (replaces validateDepEntries/validateMCPDeps) ---
		{"candy env_require missing description rejected", "candy",
			candy(candyHead + candyPlan + "  env_require:\n  - name: FOO\n"), true},
		{"candy secret_accept invalid name rejected", "candy",
			candy(candyHead + candyPlan + "  secret_accept:\n  - name: BAD-NAME\n    description: d\n"), true},
		{"candy env_require valid accepted", "candy",
			candy(candyHead + candyPlan + "  env_require:\n  - name: FOO\n    description: d\n"), false},
		{"candy mcp_require missing description rejected", "candy",
			candy(candyHead + candyPlan + "  mcp_require:\n  - name: srv\n"), true},
		{"candy mcp_provide bad transport rejected", "candy",
			candy(candyHead + candyPlan + "  mcp_provide:\n  - name: srv\n    url: http://x\n    transport: grpc\n"), true},
		// --- cutover #9: var key shape + run-step mode (replaces validateCandyTasks var-key/mode) ---
		{"candy var bad key rejected", "candy",
			candy(candyHead + candyPlan + "  var:\n    bad-key: x\n"), true},
		{"candy var valid key accepted", "candy",
			candy(candyHead + candyPlan + "  var:\n    MY_VAR: x\n"), false},
		{"candy run step bad mode rejected", "candy",
			candy(candyHead + "  plan:\n  - run: mk\n    mkdir: /a\n    mode: \"9999\"\n"), true},
		// --- cutover #9: Op-level checks (replaces validateOps/validateCheck declarative checks) ---
		{"candy check bad timeout rejected", "candy",
			candy(candyHead + "  plan:\n  - check: c\n    http:\n      http: http://x/\n      status: 200\n    context: [runtime]\n    timeout: notaduration\n"), true},
		{"candy check bad context rejected", "candy",
			candy(candyHead + "  plan:\n  - check: c\n    file: /x\n    context: [weird]\n"), true},
		{"candy check bad matcher op rejected", "candy",
			candy(candyHead + "  plan:\n  - check: c\n    command: x\n    context: [runtime]\n    stdout:\n    - mystery: \"?\"\n"), true},
		// The former mcp/spice/libvirt bogus-method and port out-of-range REJECT
		// cases were DELETED with the live-verb/state-verb extraction: the per-verb
		// method enums and verb-exclusive field constraints left core #Op — each
		// lives in its plugin's served input def (#McpInput/#SpiceInput/#LibvirtInput/
		// #PortInput), validated at dispatch (validateAuthoredPluginInput), not by
		// the core manifest gate this test exercises.
		// --- cutover #9: candy route (replaces deleted validateRoutes) ---
		{"candy route valid accepted", "candy",
			candy(candyHead + candyPlan + "  route:\n    host: svc.localhost\n    port: 8080\n"), false},
		{"candy route missing host rejected", "candy",
			candy(candyHead + candyPlan + "  route:\n    port: 8080\n"), true},
		{"candy route bad port rejected", "candy",
			candy(candyHead + candyPlan + "  route:\n    host: svc.localhost\n    port: 99999\n"), true},
		// --- cutover #9: CalVer format (replaces deleted validateVersionFields) ---
		{"candy bad version format rejected", "candy",
			candy("  version: not-calver\n  name: x\n  description: d\n" + candyPlan), true},
		{"candy missing version rejected", "candy",
			candy("  name: x\n  description: d\n" + candyPlan), true},
		{"candy bad status rejected", "candy",
			candy(candyHead + candyPlan + "  status: flaky\n"), true},
		{"candy extract relative dest rejected", "candy",
			candy(candyHead + candyPlan + "  extract:\n  - source: img:tag\n    path: /a\n    dest: rel\n"), true},
		{"box bad version format rejected", "box",
			"name: x\nbase: y\nversion: not-calver\n", true},
		// --- cutover #9: box jobs>=1 / podman_jobs_cap>=1 (replaces validateBuildTunables range) ---
		{"box jobs zero rejected", "box",
			"name: x\nbase: y\njobs: 0\n", true},
		{"box jobs valid accepted", "box",
			"name: x\nbase: y\njobs: 4\n", false},
		// --- cutover #9: box check_level enum (replaces validateBuildAndDistro check_level) ---
		{"box bad check_level rejected", "box",
			"name: x\nbase: y\ncheck_level: verbose\n", true},
		{"box valid check_level accepted", "box",
			"name: x\nbase: y\ncheck_level: agent\n", false},
		// --- cutover #9: distro repo name required (replaces validatePkgConfig repo-name) ---
		{"candy distro repo missing name rejected", "candy",
			candy(candyHead + candyPlan + "  distro:\n    arch:\n      package: [foo]\n      repo:\n      - server: https://x\n"), true},
		{"candy distro repo with name accepted", "candy",
			candy(candyHead + candyPlan + "  distro:\n    arch:\n      package: [foo]\n      repo:\n      - name: r\n        server: https://x\n"), false},
		// --- cutover #9: candy volume name regex / alias command / extract absolute ---
		{"candy volume bad name rejected", "candy",
			candy(candyHead + candyPlan + "  volume:\n  - name: Bad_Name\n    path: /data\n"), true},
		{"candy volume valid accepted", "candy",
			candy(candyHead + candyPlan + "  volume:\n  - name: my-data\n    path: /data\n"), false},
		{"candy volume missing name rejected", "candy",
			candy(candyHead + candyPlan + "  volume:\n  - path: /data\n"), true},
		{"candy volume missing path rejected", "candy",
			candy(candyHead + candyPlan + "  volume:\n  - name: my-data\n"), true},
		{"candy alias missing command rejected", "candy",
			candy(candyHead + candyPlan + "  alias:\n  - name: foo\n"), true},
		{"candy alias missing name rejected", "candy",
			candy(candyHead + candyPlan + "  alias:\n  - command: /bin/foo\n"), true},
		{"candy alias with command accepted", "candy",
			candy(candyHead + candyPlan + "  alias:\n  - name: foo\n    command: /bin/foo\n"), false},
		{"candy extract relative path rejected", "candy",
			candy(candyHead + candyPlan + "  extract:\n  - source: img:tag\n    path: rel/x\n    dest: /opt/x\n"), true},
		{"candy extract absolute accepted", "candy",
			candy(candyHead + candyPlan + "  extract:\n  - source: img:tag\n    path: /usr/bin/x\n    dest: /opt/x\n"), false},

		// --- Part A: VM/libvirt validator deletion — coverage migrated from the
		//     deleted Go VM/libvirt validators (formerly vfio_test.go / vm_snapshot_test.go). ---
		// hostdev pci: hex source domain/bus/slot/function + managed enum
		{"vm hostdev pci full hex accepted", "vm",
			"source:\n  kind: cloud_image\n  url: http://x\nlibvirt:\n  devices:\n    hostdevs:\n    - type: pci\n      managed: \"yes\"\n      source:\n        domain: \"0x0000\"\n        bus: \"0x01\"\n        slot: \"0x00\"\n        function: \"0x0\"\n", false},
		{"vm hostdev bad managed rejected", "vm",
			"source:\n  kind: cloud_image\n  url: http://x\nlibvirt:\n  devices:\n    hostdevs:\n    - type: pci\n      managed: maybe\n      source:\n        domain: \"0x0000\"\n        bus: \"0x01\"\n        slot: \"0x00\"\n        function: \"0x0\"\n", true},
		{"vm hostdev pci missing slot/function rejected", "vm",
			"source:\n  kind: cloud_image\n  url: http://x\nlibvirt:\n  devices:\n    hostdevs:\n    - type: pci\n      source:\n        domain: \"0x0000\"\n        bus: \"0x01\"\n", true},
		{"vm hostdev pci non-hex bus rejected", "vm",
			"source:\n  kind: cloud_image\n  url: http://x\nlibvirt:\n  devices:\n    hostdevs:\n    - type: pci\n      source:\n        domain: \"0x0000\"\n        bus: zz\n        slot: \"0x00\"\n        function: \"0x0\"\n", true},
		// filesystem: driver/accessmode enums + source/target required
		{"vm filesystem virtiofs accepted", "vm",
			"source:\n  kind: cloud_image\n  url: http://x\nlibvirt:\n  devices:\n    filesystems:\n    - driver: virtiofs\n      accessmode: passthrough\n      source: /home/user\n      target: workspace\n", false},
		{"vm filesystem bad driver rejected", "vm",
			"source:\n  kind: cloud_image\n  url: http://x\nlibvirt:\n  devices:\n    filesystems:\n    - driver: nfs\n      source: /h\n      target: w\n", true},
		{"vm filesystem bad accessmode rejected", "vm",
			"source:\n  kind: cloud_image\n  url: http://x\nlibvirt:\n  devices:\n    filesystems:\n    - accessmode: weird\n      source: /h\n      target: w\n", true},
		{"vm filesystem missing source rejected", "vm",
			"source:\n  kind: cloud_image\n  url: http://x\nlibvirt:\n  devices:\n    filesystems:\n    - driver: virtiofs\n      target: w\n", true},
		{"vm filesystem missing target rejected", "vm",
			"source:\n  kind: cloud_image\n  url: http://x\nlibvirt:\n  devices:\n    filesystems:\n    - driver: virtiofs\n      source: /h\n", true},
		// autostart ⇒ libvirt backend (qemu has no persistent daemon)
		{"vm autostart qemu rejected", "vm",
			"source:\n  kind: cloud_image\n  url: http://x\nautostart: true\nbackend: qemu\n", true},
		{"vm autostart libvirt accepted", "vm",
			"source:\n  kind: cloud_image\n  url: http://x\nautostart: true\nbackend: libvirt\n", false},
		{"vm autostart default-backend accepted", "vm",
			"source:\n  kind: cloud_image\n  url: http://x\nautostart: true\n", false},
		// source clone: from_vm + from_snapshot required, cross-arm url forbidden
		{"vm source clone valid accepted", "vm",
			"source:\n  kind: clone\n  from_vm: arch\n  from_snapshot: baseline\n", false},
		{"vm source clone missing from_vm rejected", "vm",
			"source:\n  kind: clone\n  from_snapshot: baseline\n", true},
		{"vm source clone missing from_snapshot rejected", "vm",
			"source:\n  kind: clone\n  from_vm: arch\n", true},
		{"vm source clone with url rejected", "vm",
			"source:\n  kind: clone\n  from_vm: arch\n  from_snapshot: baseline\n  url: http://x\n", true},
		// source imported: libvirt_name/disk_path required, disk_format enum
		{"vm source imported valid accepted", "vm",
			"source:\n  kind: imported\n  libvirt_name: my-vm\n  disk_path: /var/x.qcow2\n  disk_format: qcow2\n", false},
		{"vm source imported missing libvirt_name rejected", "vm",
			"source:\n  kind: imported\n  disk_path: /x\n  disk_format: qcow2\n", true},
		{"vm source imported bad disk_format rejected", "vm",
			"source:\n  kind: imported\n  libvirt_name: x\n  disk_path: /x\n  disk_format: vmdk\n", true},
		// the smm GAP closed this cutover: uefi-secure ⇒ libvirt.features.smm:true
		{"vm uefi-secure without smm rejected", "vm",
			"source:\n  kind: cloud_image\n  url: http://x\nfirmware: uefi-secure\n", true},
		{"vm uefi-secure with smm accepted", "vm",
			"source:\n  kind: cloud_image\n  url: http://x\nfirmware: uefi-secure\nlibvirt:\n  features:\n    smm: true\n", false},
		{"vm uefi-secure i440fx rejected", "vm",
			"source:\n  kind: cloud_image\n  url: http://x\nfirmware: uefi-secure\nmachine: i440fx\nlibvirt:\n  features:\n    smm: true\n", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var err error
			if tc.kind == "candy" {
				err = validateCandyManifestCUE("t.yml", []byte(tc.yaml))
			} else {
				err = validateKindBody(t, tc.kind, tc.yaml)
			}
			if tc.reject && err == nil {
				t.Errorf("expected CUE to REJECT %q, but it passed", tc.name)
			}
			if !tc.reject && err != nil {
				t.Errorf("expected CUE to ACCEPT %q, but it failed: %v", tc.name, err)
			}
		})
	}
}
