package vm

// provider.go is the OUT-OF-PROCESS vm-plugin provider's Invoke. The `libvirt`
// check verb dispatches here (the `libvirt: <method>` sugar desugars to plugin/plugin_input —
// the method + every libvirt-exclusive modifier ride the input map, decoded into
// params.LibvirtVerbInput; authoring unchanged), and Invoke OWNS the verdict — it runs the
// in-process LibvirtCmd Kong tree (which carries the go-libvirt impl this plugin extracted) and
// self-evaluates the matchers, exactly like candy/plugin-kube. The internal VM-resolution + the
// lifecycle ops (resolve-target / domain-state / list-domains / create / start / stop …) are
// all wired below alongside the libvirt verb.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	libvirt "github.com/digitalocean/go-libvirt"

	"github.com/opencharly/charly/candy/plugin-vm/params"
	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/kit"
	pb "github.com/opencharly/sdk/proto"
	"github.com/opencharly/sdk/spec"
)

// vmEnv is the plugin-side decode of the host's CheckEnv (for a `libvirt:` check — the run mode
// plus the VM/deploy name the host appends as the verb's positional, mirroring the old
// host-side r.Box append) AND the internal VM-resolution RPC (when VmOp is set — the host's
// invokeVmPlugin path the spice/vnc/ssh/status/preempt consumers call, bypassing the verb
// pipeline). Exactly one of {Mode/Box for a verb} / {VmOp for an internal op} is meaningful.
type vmEnv struct {
	Box  string `json:"box"`
	Mode string `json:"mode"` // "live" | "box"
	// VmOp selects an internal (non-verb) VM-resolution op: "domain-state" | "list-domains" |
	// "resolve-spice" | "resolve-vnc". Empty for a `libvirt:` verb check.
	VmOp       string `json:"vm_op,omitempty"`
	VmName     string `json:"vm_name,omitempty"`     // the full libvirt domain name (charly-<vm>) for domain-state
	URI        string `json:"uri,omitempty"`         // libvirt URI ("" = local qemu:///session)
	Force      bool   `json:"force,omitempty"`       // stop: destroy vs graceful shutdown
	DeleteDisk bool   `json:"delete_disk,omitempty"` // destroy: undefine with storage removal
	// Create carries the fully host-resolved create request (the host did override/GPU/defaults/
	// disk/seed/SSH/smbios/OVMF resolution; the plugin just renders + creates the domain/process).
	Create *vmCreateReq `json:"create,omitempty"`
	// Snap carries a snapshot-internal op — the host orchestration (refcount/ledger in vm_snapshot.go)
	// stays core; only the go-libvirt internal func runs here.
	Snap *vmSnapInternalReq `json:"snap,omitempty"`
}

// vmSnapInternalReq is the snapshot-internal op payload (the go-libvirt internal snapshot funcs).
type vmSnapInternalReq struct {
	SnapOp  string              `json:"snap_op"` // create | delete | revert | promote
	VmName  string              `json:"vm_name"`
	Opts    *SnapshotCreateOpts `json:"opts,omitempty"`
	Entry   *SnapshotEntry      `json:"entry,omitempty"`
	OutPath string              `json:"out_path,omitempty"`
}

// vmCreateReq is the host-resolved create payload (matches charly's vmCreateReq). Spec + RT are
// fully materialized host-side; runVmSpecCreateLibvirt/Qemu render + create with them.
type vmCreateReq struct {
	Spec         *VmSpec         `json:"spec"`
	RT           VmRuntimeParams `json:"rt"`
	VmDomainName string          `json:"vm_domain_name"`
	Home         string          `json:"home"`
	VmName       string          `json:"vm_name"`
	Name         string          `json:"name"`
	Backend      string          `json:"backend"`
	VmStateDir   string          `json:"vm_state_dir"`
	// ValidateOnly: render + RETURN the libvirt domain XML for host-side egress
	// validation (ValidateXMLEgress) WITHOUT creating; the host then issues a
	// second create call. Matches charly's vmCreateReq.
	ValidateOnly bool `json:"validate_only,omitempty"`
}

// Invoke runs one `libvirt:` verb method against the in-process libvirt impl and self-evaluates
// the authored matchers (mirrors the former host-side matcher pipeline).
func (vmProvider) Invoke(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	// command:vm — the `charly vm …` CLI (compiled-in, in-proc). Dispatched via Invoke(OpRun) with
	// Reserved=="vm": recover the reverse-channel executor + run the VmCmd tree (command.go). In-proc
	// dispatch means the handlers inherit charly's real stdio/TTY (console/ssh stay interactive).
	if req.GetOp() == sdk.OpRun && req.GetReserved() == "vm" {
		return runVmCommand(ctx, req)
	}

	var op spec.Op
	if len(req.GetParamsJson()) > 0 {
		if err := json.Unmarshal(req.GetParamsJson(), &op); err != nil {
			return sdk.ResultJSON("fail", "libvirt: decode op: "+err.Error())
		}
	}
	var env vmEnv
	if len(req.GetEnvJson()) > 0 {
		if err := json.Unmarshal(req.GetEnvJson(), &env); err != nil {
			return sdk.ResultJSON("fail", "libvirt: decode env: "+err.Error())
		}
	}

	// Internal VM-resolution RPC (the host's invokeVmPlugin path) — NOT a `libvirt:` verb check;
	// returns the structured result the consumer decodes, bypassing the matcher pipeline.
	if env.VmOp != "" {
		return dispatchInternalOp(env)
	}

	// The verb's method + libvirt-exclusive fields ride the desugared plugin input
	// since the schema-compaction cutover (params.LibvirtVerbInput).
	var in params.LibvirtVerbInput
	kit.DecodeInput(op.PluginInput, &in)
	method := in.Method

	// libvirt probes a running VM — skip under `charly check box` (no live domain on a
	// disposable build container), mirroring the host's RunModeBox/box-mode skip.
	if env.Mode == "box" {
		return sdk.ResultJSON("skip", fmt.Sprintf("libvirt: %s requires a running VM (skip under charly check box)", method))
	}

	out, capturedStderr, runErr := dispatchLibvirtVerb(&op, &in, env.Box)

	exit := 0
	stderr := capturedStderr // the verb's real os.Stderr (e.g. guest/ping's "agent responsive")
	if runErr != nil {
		exit = 1
		if stderr != "" {
			stderr += "\n"
		}
		stderr += runErr.Error()
	}
	wantExit := 0
	if op.ExitStatus != nil {
		wantExit = *op.ExitStatus
	}
	if exit != wantExit {
		return sdk.ResultJSON("fail", fmt.Sprintf("libvirt: %s: exit=%d, want %d (stderr: %s)", method, exit, wantExit, sdk.Preview(stderr)))
	}
	if err := sdk.MatchAll(out, op.Stdout); err != nil {
		return sdk.ResultJSON("fail", fmt.Sprintf("libvirt: %s: stdout: %v (got: %s)", method, err, sdk.Preview(out)))
	}
	if err := sdk.MatchAll(stderr, op.Stderr); err != nil {
		return sdk.ResultJSON("fail", fmt.Sprintf("libvirt: %s: stderr: %v (got: %s)", method, err, sdk.Preview(stderr)))
	}

	body := out
	if strings.TrimSpace(body) == "" {
		body = stderr
	}
	if strings.TrimSpace(body) == "" {
		body = fmt.Sprintf("libvirt %s: exit=%d", method, exit)
	}
	return sdk.ResultJSON("pass", body)
}

// dispatchLibvirtVerb runs one libvirt method through the in-process LibvirtCmd Kong tree,
// reusing the libvirtMethods allowlist (method→subcommand-path + positional args) so the nested
// guest/* + snapshot/* subgroups dispatch without a per-method switch. The kit.Pos* builders
// read the plugin-input map off the op; the snapshot methods read the SHARED step-level
// op.Target. Returns captured stdout + stderr.
func dispatchLibvirtVerb(op *spec.Op, in *params.LibvirtVerbInput, box string) (stdout, stderr string, err error) {
	ms, ok := libvirtMethods[in.Method]
	if !ok {
		return "", "", fmt.Errorf("unknown libvirt method %q", in.Method)
	}
	args := append([]string{}, ms.Path[1:]...) // drop the leading "libvirt"
	if !ms.SkipBox {
		args = append(args, box) // the VM positional (the deploy/vm name)
	}
	if ms.PosArgs != nil {
		args = append(args, ms.PosArgs(op)...)
	}
	if in.URI != "" {
		args = append(args, "--uri", in.URI)
	}
	return captureOutput(func() error {
		var cli LibvirtCmd
		return sdk.RunInProcCLI("libvirt", &cli, args)
	})
}

// captureMu serializes the os.Stdout/os.Stderr redirect in captureOutput — verb Invokes can
// overlap and the redirect is process-global.
var captureMu sync.Mutex

// captureOutput runs fn with BOTH os.Stdout and os.Stderr redirected to pipes and returns what
// each wrote. The libvirt handlers print verb output to os.Stdout (the stdout: matcher target)
// AND some print status lines to os.Stderr (e.g. guest/ping's "agent responsive" — the stderr:
// matcher target), so both streams must be captured for a plan step's stdout:/stderr: matchers
// to see the out-of-process verb's real output. (Check-verb output is small — well under the
// pipe buffer — so the read-after-close pattern cannot deadlock here.)
func captureOutput(fn func() error) (stdout, stderr string, err error) {
	captureMu.Lock()
	defer captureMu.Unlock()
	oldOut, oldErr := os.Stdout, os.Stderr
	rOut, wOut, perr := os.Pipe()
	if perr != nil {
		return "", "", perr
	}
	rErr, wErr, perr := os.Pipe()
	if perr != nil {
		_ = wOut.Close()
		_ = rOut.Close()
		return "", "", perr
	}
	os.Stdout, os.Stderr = wOut, wErr
	runErr := fn()
	_ = wOut.Close()
	_ = wErr.Close()
	os.Stdout, os.Stderr = oldOut, oldErr
	outData, _ := io.ReadAll(rOut)
	errData, _ := io.ReadAll(rErr)
	_ = rOut.Close()
	_ = rErr.Close()
	return string(outData), string(errData), runErr
}

// dispatchInternalOp handles a host-initiated VM-resolution RPC (env.VmOp) and returns the
// structured JSON result the host's invokeVmPlugin decodes. The stateful libvirt connection stays
// IN the plugin (it cannot cross the process boundary); the host sends a high-level op + args.
// resolve-spice/resolve-vnc (the DisplayEndpoint path) are handled below.
func dispatchInternalOp(env vmEnv) (*pb.InvokeReply, error) {
	uri := env.URI
	if uri == "" {
		uri = libvirtSessionURI
	}
	switch env.VmOp {
	case "domain-state":
		conn, err := connectLibvirt(uri)
		if err != nil {
			return internalJSON(map[string]any{"exists": false, "running": false, "error": err.Error()})
		}
		defer conn.Close() //nolint:errcheck
		dom, err := conn.lookupDomain(env.VmName)
		if err != nil {
			return internalJSON(map[string]any{"exists": false, "running": false})
		}
		st, _ := conn.domainState(dom)
		return internalJSON(map[string]any{"exists": true, "running": st == libvirt.DomainRunning})

	case "list-domains":
		conn, err := connectLibvirt(uri)
		if err != nil {
			return internalJSON(map[string]any{"error": err.Error()})
		}
		defer conn.Close() //nolint:errcheck
		doms, err := conn.listCharlyDomains()
		if err != nil {
			return internalJSON(map[string]any{"error": err.Error()})
		}
		return internalJSON(doms)

	case "resolve-spice", "resolve-vnc":
		t, err := ResolveVmTarget(env.VmName, uri)
		if err != nil {
			return internalJSON(map[string]any{"error": err.Error()})
		}
		tunnelTarget := t.Uri
		var ep DisplayEndpoint
		var epErr error
		if env.VmOp == "resolve-spice" {
			ep, epErr = t.SpiceEndpoint()
		} else {
			ep, epErr = t.VncEndpoint()
		}
		_ = t.Close() //nolint:errcheck
		// The host decodes {endpoint, error, tunnel_target} and does the skip / dialable-endpoint
		// / tunnel logic (the tunnel + the no-display-device skip stay host-side; tunnel_target is
		// the libvirt URI's remote for a qemu+ssh:// VM, empty for a local one).
		res := map[string]any{"tunnel_target": tunnelTarget}
		if epErr != nil {
			res["error"] = epErr.Error()
		} else {
			res["endpoint"] = ep
		}
		return internalJSON(res)

	case "start", "stop", "destroy":
		conn, err := connectLibvirt(uri)
		if err != nil {
			return internalJSON(map[string]any{"error": err.Error()})
		}
		defer conn.Close() //nolint:errcheck
		dom, err := conn.lookupDomain(env.VmName)
		if err != nil {
			// destroy is idempotent: a missing domain is "already gone", a clean success.
			if env.VmOp == "destroy" {
				return internalJSON(map[string]any{"ok": true, "already_gone": true})
			}
			return internalJSON(map[string]any{"error": "domain not found: " + err.Error()})
		}
		switch env.VmOp {
		case "start":
			// start is idempotent, exactly as destroy is above: an already-running
			// domain is a clean success, not an error. libvirt otherwise fails with
			// "Requested operation is not valid: domain is already running", which
			// forced every caller that only wants "ensure running" (vmRebuild, after
			// `vm create` has already started the domain) to discard the error and
			// print it to the log — noise that trains readers to skim past real ones.
			if s, serr := conn.domainState(dom); serr == nil && s == libvirt.DomainRunning {
				return internalJSON(map[string]any{"ok": true, "already_running": true})
			}
			err = conn.startDomain(dom)
		case "stop":
			if env.Force {
				_ = conn.destroyDomain(dom) //nolint:errcheck
			} else {
				err = conn.shutdownDomain(dom)
			}
		case "destroy":
			conn.gracefulStopDomain(dom)
			err = conn.undefineDomain(dom, env.DeleteDisk)
		}
		if err != nil {
			return internalJSON(map[string]any{"error": err.Error()})
		}
		return internalJSON(map[string]any{"ok": true})

	case "create":
		r := env.Create
		if r == nil {
			return internalJSON(map[string]any{"error": "create: missing request payload"})
		}
		// ValidateOnly: render the libvirt domain XML and RETURN it for host-side
		// egress validation (the out-of-process plugin no-ops egress; the host runs
		// the real ValidateXMLEgress, then issues a second create call). QEMU has no
		// domain XML (its cloud-init is already egress-validated host-side via the
		// host's RegenerateSeedISO), so the validate pass returns empty.
		if r.ValidateOnly {
			if r.Backend == "libvirt" {
				xmlStr, xerr := RenderDomainXML(r.Spec, r.RT)
				if xerr != nil {
					return internalJSON(map[string]any{"error": xerr.Error()})
				}
				return internalJSON(map[string]any{"ok": true, "rendered_domain_xml": xmlStr})
			}
			return internalJSON(map[string]any{"ok": true})
		}
		var cerr error
		switch r.Backend {
		case "libvirt":
			cerr = runVmSpecCreateLibvirt(r.Spec, r.RT, r.VmDomainName, r.Home, r.VmName, r.Name)
		case "qemu":
			cerr = runVmSpecCreateQemu(r.Spec, r.RT, r.VmDomainName, r.Home, r.VmName, r.Name, r.VmStateDir)
		default:
			cerr = fmt.Errorf("unknown backend %q", r.Backend)
		}
		if cerr != nil {
			return internalJSON(map[string]any{"error": cerr.Error()})
		}
		return internalJSON(map[string]any{"ok": true})

	case "snapshot-internal":
		r := env.Snap
		if r == nil {
			return internalJSON(map[string]any{"error": "snapshot-internal: missing payload"})
		}
		var serr error
		switch r.SnapOp {
		case "create":
			if r.Opts == nil {
				serr = fmt.Errorf("snapshot create: missing opts")
			} else {
				serr = createInternalSnapshot(*r.Opts)
			}
		case "delete":
			serr = deleteInternalSnapshot(r.VmName, r.Entry)
		case "revert":
			serr = revertInternalSnapshot(r.VmName, r.Entry)
		case "promote":
			serr = promoteInternalToExternal(r.VmName, r.Entry, r.OutPath)
		case "create-external":
			if r.Opts == nil {
				serr = fmt.Errorf("snapshot create-external: missing opts")
			} else {
				serr = createExternalSnapshot(*r.Opts, r.OutPath)
			}
		case "delete-external":
			serr = deleteExternalSnapshot(r.VmName, r.Entry)
		case "revert-external":
			serr = revertExternalSnapshot(r.VmName, r.Entry)
		default:
			serr = fmt.Errorf("unknown snapshot op %q", r.SnapOp)
		}
		if serr != nil {
			return internalJSON(map[string]any{"error": serr.Error()})
		}
		return internalJSON(map[string]any{"ok": true})

	case "domain-xml":
		conn, err := connectLibvirt(uri)
		if err != nil {
			return internalJSON(map[string]any{"error": err.Error()})
		}
		defer conn.Close() //nolint:errcheck
		dom, err := conn.lookupDomain(env.VmName)
		if err != nil {
			return internalJSON(map[string]any{"error": "domain not found: " + err.Error()})
		}
		xmlStr, err := conn.l.DomainGetXMLDesc(dom, 0)
		if err != nil {
			return internalJSON(map[string]any{"error": err.Error()})
		}
		return internalJSON(map[string]any{"xml": xmlStr})

	case "list-all-domains":
		conn, err := connectLibvirt(uri)
		if err != nil {
			return internalJSON(map[string]any{"error": err.Error()})
		}
		defer conn.Close() //nolint:errcheck
		doms, _, err := conn.l.ConnectListAllDomains(1, 0)
		if err != nil {
			return internalJSON(map[string]any{"error": err.Error()})
		}
		names := make([]string, 0, len(doms))
		for _, d := range doms {
			names = append(names, d.Name)
		}
		return internalJSON(map[string]any{"names": names})

	default:
		return internalJSON(map[string]any{"error": "unknown internal vm op: " + env.VmOp})
	}
}

func internalJSON(v any) (*pb.InvokeReply, error) {
	j, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return &pb.InvokeReply{ResultJson: j}, nil
}
