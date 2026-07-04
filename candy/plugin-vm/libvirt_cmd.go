package vm

// `charly check libvirt <vm-name> <verb>` — the Kong command tree.
//
// Every verb is a thin wrapper over go-libvirt RPCs + libvirtxml for
// XML parsing. Shared helpers live in libvirt_ops.go (screenshot
// stream drain, passwd XML patch, event subscribe); QGA client lives
// in libvirt_guest_agent.go.

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"image/png"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	libvirt "github.com/digitalocean/go-libvirt"
	libvirtxml "libvirt.org/go/libvirtxml"
)

// libvirtURIFlag carries the --uri flag shared by every Libvirt
// subcommand. Empty string means the local qemu:///session (default
// behavior); qemu+ssh://[user@]host[:port]/session connects over SSH
// via the SSH tunnel machinery in charly/ssh_tunnel.go.
//
// Also honored as the CHARLY_LIBVIRT_URI environment variable.
type libvirtURIFlag struct {
	Uri string `name:"uri" env:"CHARLY_LIBVIRT_URI" help:"Libvirt URI (default: qemu:///session). Use qemu+ssh://[user@]host/session for remote hypervisors."`
}

// LibvirtCmd groups all libvirt-RPC test verbs.
type LibvirtCmd struct {
	// Top-level verbs
	List       LibvirtListCmd       `cmd:"" help:"List all libvirt domains on the session"`
	Info       LibvirtInfoCmd       `cmd:"" help:"Show domain state, graphics, uptime"`
	Screenshot LibvirtScreenshotCmd `cmd:"" help:"Capture VM framebuffer via DomainScreenshot"`
	SendKey    LibvirtSendKeyCmd    `cmd:"" name:"send-key" help:"Inject keyboard events via DomainSendKey (alt path)"`
	Passwd     LibvirtPasswdCmd     `cmd:"" help:"Set live graphics password (SPICE/VNC)"`
	Qmp        LibvirtQmpCmd        `cmd:"" help:"Send raw QMP command to the domain"`
	DomainXML  LibvirtDomainXMLCmd  `cmd:"" name:"domain-xml" help:"Dump the live domain XML"`
	Console    LibvirtConsoleCmd    `cmd:"" help:"Tail serial console"`
	Events     LibvirtEventsCmd     `cmd:"" help:"Watch domain lifecycle events"`

	// Subgroups
	Guest    LibvirtGuestGroup    `cmd:"" help:"qemu-guest-agent client (ping/info/exec/file/fsfreeze)"`
	Snapshot LibvirtSnapshotGroup `cmd:"" help:"VM snapshots (list/create/revert/delete)"`
}

// ---------------- list ----------------

type LibvirtListCmd struct {
	Format string `long:"format" default:"text" help:"Output format: text, json"`
	libvirtURIFlag
}

func (c *LibvirtListCmd) Run() error {
	conn, err := connectLibvirt(c.Uri)
	if err != nil {
		return err
	}
	defer conn.Close() //nolint:errcheck

	flags := libvirt.ConnectListDomainsActive | libvirt.ConnectListDomainsInactive
	doms, _, err := conn.l.ConnectListAllDomains(1, flags)
	if err != nil {
		return fmt.Errorf("listing domains: %w", err)
	}

	type row struct {
		Name  string `json:"name"`
		State string `json:"state"`
		Uuid  string `json:"uuid"`
		Id    int32  `json:"id"`
	}
	var rows []row
	for _, d := range doms {
		state, _, serr := conn.l.DomainGetState(d, 0)
		s := "unknown"
		if serr == nil {
			s = domainStateString(libvirt.DomainState(state))
		}
		uuidHex := fmt.Sprintf("%x", d.UUID[:])
		rows = append(rows, row{Name: d.Name, State: s, Uuid: uuidHex, Id: d.ID})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })

	if c.Format == "json" {
		return writeJSON(os.Stdout, rows)
	}
	fmt.Printf("%-40s  %-12s  %s\n", "NAME", "STATE", "UUID")
	for _, r := range rows {
		fmt.Printf("%-40s  %-12s  %s\n", r.Name, r.State, r.Uuid)
	}
	return nil
}

// ---------------- info ----------------

type LibvirtInfoCmd struct {
	Vm     string `arg:"" help:"VM name (vm.yml entity)"`
	Format string `long:"format" default:"text" help:"Output format: text, json"`
	libvirtURIFlag
}

func (c *LibvirtInfoCmd) Run() error {
	t, err := ResolveVmTarget(c.Vm, c.Uri)
	if err != nil {
		return err
	}
	defer t.Close() //nolint:errcheck

	state, _, err := t.Conn.l.DomainGetState(t.Domain, 0)
	if err != nil {
		return fmt.Errorf("getting state: %w", err)
	}
	_, maxMem, memory, nrCPU, cpuTime, err := t.Conn.l.DomainGetInfo(t.Domain)
	if err != nil {
		return fmt.Errorf("getting info: %w", err)
	}

	type graphicsInfo struct {
		Type     string `json:"type"`
		Port     int    `json:"port"`
		Listen   string `json:"listen"`
		AutoPort string `json:"autoport,omitempty"`
		Passwd   bool   `json:"passwd_set"`
	}
	var graphics []graphicsInfo
	if t.XML != nil && t.XML.Devices != nil {
		for _, g := range t.XML.Devices.Graphics {
			if g.Spice != nil {
				gi := graphicsInfo{
					Type:     "spice",
					Port:     g.Spice.Port,
					AutoPort: g.Spice.AutoPort,
					Passwd:   g.Spice.Passwd != "",
				}
				gi.Listen = formatGraphicsListen(g.Spice.Listeners)
				graphics = append(graphics, gi)
			}
			if g.VNC != nil {
				gi := graphicsInfo{
					Type:     "vnc",
					Port:     g.VNC.Port,
					AutoPort: g.VNC.AutoPort,
					Passwd:   g.VNC.Passwd != "",
				}
				gi.Listen = formatGraphicsListen(g.VNC.Listeners)
				if gi.Listen == "" && g.VNC.Listen != "" {
					gi.Listen = g.VNC.Listen
				}
				graphics = append(graphics, gi)
			}
		}
	}

	out := map[string]any{
		"vm":              c.Vm,
		"domain":          t.DomName,
		"state":           domainStateString(libvirt.DomainState(state)),
		"max_mem":         maxMem,
		"memory":          memory,
		"cpus":            nrCPU,
		"cpu_time":        cpuTime,
		"graphics":        graphics,
		"agent_reachable": t.AgentReachable(3 * time.Second),
	}

	if c.Format == "json" {
		return writeJSON(os.Stdout, out)
	}
	fmt.Printf("Name:      %s  (domain: %s)\n", c.Vm, t.DomName)
	fmt.Printf("State:     %s\n", out["state"])
	fmt.Printf("Memory:    %d / %d MiB\n", memory/1024, maxMem/1024)
	fmt.Printf("vCPUs:     %d\n", nrCPU)
	fmt.Printf("CPU time:  %s\n", time.Duration(cpuTime).Round(time.Second))
	fmt.Printf("Agent:     %v\n", out["agent_reachable"])
	if len(graphics) == 0 {
		fmt.Printf("Graphics:  (none)\n")
	}
	for _, g := range graphics {
		passwd := ""
		if g.Passwd {
			passwd = " passwd=set"
		}
		fmt.Printf("Graphics:  %s %s:%d autoport=%s%s\n", g.Type, g.Listen, g.Port, g.AutoPort, passwd)
	}
	return nil
}

// ---------------- screenshot ----------------

type LibvirtScreenshotCmd struct {
	Vm     string `arg:"" help:"VM name"`
	File   string `arg:"" optional:"" default:"screenshot.png" help:"Output file path (use '-' for stdout)"`
	Screen int    `long:"screen" default:"0" help:"Display index for multi-head VMs"`
	libvirtURIFlag
}

func (c *LibvirtScreenshotCmd) Run() error {
	t, err := ResolveVmTarget(c.Vm, c.Uri)
	if err != nil {
		return err
	}
	defer t.Close() //nolint:errcheck
	if err := t.EnsureRunning(); err != nil {
		return err
	}

	img, err := captureDomainScreenshot(t.Conn.l, t.Domain, uint(c.Screen))
	if err != nil {
		return fmt.Errorf("screenshot: %w", err)
	}
	w, closeFn, err := openOutputPath(c.File)
	if err != nil {
		return fmt.Errorf("creating %s: %w", c.File, err)
	}
	if err := png.Encode(w, img); err != nil {
		_ = closeFn()
		return fmt.Errorf("encoding PNG: %w", err)
	}
	if err := closeFn(); err != nil {
		return err
	}
	b := img.Bounds()
	dest := c.File
	if dest == "-" {
		dest = "stdout"
	}
	fmt.Fprintf(os.Stderr, "Screenshot saved to %s (%dx%d, via libvirt DomainScreenshot)\n",
		dest, b.Dx(), b.Dy())
	return nil
}

// ---------------- send-key ----------------

type LibvirtSendKeyCmd struct {
	Vm   string   `arg:"" help:"VM name"`
	Keys []string `arg:"" help:"Key names (space-separated, e.g. 'ctrl alt F2')"`
	Hold int      `long:"hold" default:"50" help:"Hold duration in ms"`
	libvirtURIFlag
}

func (c *LibvirtSendKeyCmd) Run() error {
	t, err := ResolveVmTarget(c.Vm, c.Uri)
	if err != nil {
		return err
	}
	defer t.Close() //nolint:errcheck
	if err := t.EnsureRunning(); err != nil {
		return err
	}
	codes, err := mapLibvirtKeys(c.Keys)
	if err != nil {
		return err
	}
	// codeset 1 = Linux keycode set.
	if err := t.Conn.l.DomainSendKey(t.Domain, 1, uint32(c.Hold), codes, 0); err != nil {
		return fmt.Errorf("DomainSendKey: %w", err)
	}
	fmt.Fprintf(os.Stderr, "Sent %d key(s) to %s: %s\n", len(codes), t.DomName, strings.Join(c.Keys, "+"))
	return nil
}

// ---------------- passwd ----------------

type LibvirtPasswdCmd struct {
	Vm       string `arg:"" help:"VM name"`
	Password string `arg:"" optional:"" help:"Password (empty = prompt or stdin)"`
	Type     string `long:"type" default:"spice" help:"Graphics type: spice or vnc"`
	Persist  bool   `long:"persistent" help:"Also persist to inactive config (default: live only)"`
	libvirtURIFlag
}

func (c *LibvirtPasswdCmd) Run() error {
	t, err := ResolveVmTarget(c.Vm, c.Uri)
	if err != nil {
		return err
	}
	defer t.Close() //nolint:errcheck
	if err := t.EnsureRunning(); err != nil {
		return err
	}

	// Find the matching <graphics> element, patch Passwd, re-marshal
	// just that sub-element, call UpdateDeviceFlags.
	var patched *libvirtxml.DomainGraphic
	for i, g := range t.XML.Devices.Graphics {
		switch c.Type {
		case "spice":
			if g.Spice != nil {
				g.Spice.Passwd = c.Password
				patched = &t.XML.Devices.Graphics[i]
			}
		case "vnc":
			if g.VNC != nil {
				g.VNC.Passwd = c.Password
				patched = &t.XML.Devices.Graphics[i]
			}
		}
		if patched != nil {
			break
		}
	}
	if patched == nil {
		return fmt.Errorf("VM %s has no %s graphics device", c.Vm, c.Type)
	}
	out, err := xml.MarshalIndent(patched, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling patch: %w", err)
	}
	flags := libvirt.DomainDeviceModifyLive
	if c.Persist {
		flags |= libvirt.DomainDeviceModifyConfig
	}
	if err := t.Conn.l.DomainUpdateDeviceFlags(t.Domain, string(out), flags); err != nil {
		return fmt.Errorf("UpdateDeviceFlags: %w", err)
	}
	fmt.Fprintf(os.Stderr, "Updated %s password on %s (live%s)\n",
		c.Type, t.DomName, persistStr(c.Persist))
	return nil
}

func persistStr(persist bool) string {
	if persist {
		return " + config"
	}
	return ""
}

// formatGraphicsListen renders a list of libvirt <listen> children
// into a single human-readable string for `charly check libvirt info`.
// Address listeners show as "1.2.3.4"; socket listeners show as
// "unix://<path>"; multiple listeners are comma-separated.
func formatGraphicsListen(listeners []libvirtxml.DomainGraphicListener) string {
	if len(listeners) == 0 {
		return ""
	}
	parts := make([]string, 0, len(listeners))
	for _, l := range listeners {
		switch {
		case l.Address != nil && l.Address.Address != "":
			parts = append(parts, l.Address.Address)
		case l.Socket != nil:
			if l.Socket.Socket != "" {
				parts = append(parts, "unix://"+l.Socket.Socket)
			} else {
				parts = append(parts, "unix://(auto)")
			}
		case l.Network != nil:
			parts = append(parts, "network:"+l.Network.Network)
		}
	}
	return strings.Join(parts, ",")
}

// ---------------- qmp ----------------

type LibvirtQmpCmd struct {
	Vm      string `arg:"" help:"VM name"`
	Command string `arg:"" help:"QMP command name (e.g. query-status)"`
	Args    string `arg:"" optional:"" help:"JSON args (optional)"`
	libvirtURIFlag
}

func (c *LibvirtQmpCmd) Run() error {
	t, err := ResolveVmTarget(c.Vm, c.Uri)
	if err != nil {
		return err
	}
	defer t.Close() //nolint:errcheck

	req := map[string]any{"execute": c.Command}
	if c.Args != "" {
		var args any
		if err := json.Unmarshal([]byte(c.Args), &args); err != nil {
			return fmt.Errorf("parsing JSON args: %w", err)
		}
		req["arguments"] = args
	}
	buf, err := json.Marshal(req)
	if err != nil {
		return err
	}
	// Flag 0 = QMP (default), 1 = HMP.
	rep, err := t.Conn.l.QEMUDomainMonitorCommand(t.Domain, string(buf), 0)
	if err != nil {
		return fmt.Errorf("QMP: %w", err)
	}
	if rep == "" {
		fmt.Println("{}")
		return nil
	}
	// Pretty-print.
	var parsed any
	if err := json.Unmarshal([]byte(rep), &parsed); err == nil {
		out, _ := json.MarshalIndent(parsed, "", "  ")
		fmt.Println(string(out))
		return nil
	}
	fmt.Println(rep)
	return nil
}

// ---------------- domain-xml ----------------

type LibvirtDomainXMLCmd struct {
	Vm     string `arg:"" help:"VM name"`
	Config bool   `long:"config" help:"Get inactive (on-disk) config instead of live"`
	libvirtURIFlag
}

func (c *LibvirtDomainXMLCmd) Run() error {
	t, err := ResolveVmTarget(c.Vm, c.Uri)
	if err != nil {
		return err
	}
	defer t.Close() //nolint:errcheck
	var flags libvirt.DomainXMLFlags
	if c.Config {
		flags |= libvirt.DomainXMLInactive
	}
	xmlStr, err := t.Conn.l.DomainGetXMLDesc(t.Domain, flags)
	if err != nil {
		return fmt.Errorf("getting XML: %w", err)
	}
	fmt.Print(xmlStr)
	return nil
}

// ---------------- console ----------------

type LibvirtConsoleCmd struct {
	Vm       string        `arg:"" help:"VM name"`
	Duration time.Duration `long:"duration" default:"5s" help:"Stream for this duration, then exit"`
	libvirtURIFlag
}

func (c *LibvirtConsoleCmd) Run() error {
	t, err := ResolveVmTarget(c.Vm, c.Uri)
	if err != nil {
		return err
	}
	defer t.Close() //nolint:errcheck
	// go-libvirt's DomainOpenConsole + stream API is not wrapped for
	// easy tailing in this MVP. Report remediation — users fall back
	// to `virsh console <domain>` for interactive sessions.
	fmt.Fprintf(os.Stderr, "console tail not yet implemented; use: virsh -c qemu:///session console %s\n", t.DomName)
	return nil
}

// ---------------- events ----------------

type LibvirtEventsCmd struct {
	Vm       string        `arg:"" optional:"" help:"VM name (empty = all domains)"`
	Duration time.Duration `long:"duration" default:"10s" help:"Watch for this long"`
	libvirtURIFlag
}

func (c *LibvirtEventsCmd) Run() error {
	conn, err := connectLibvirt(c.Uri)
	if err != nil {
		return err
	}
	defer conn.Close() //nolint:errcheck

	// go-libvirt event subscription requires callback plumbing that
	// isn't straightforward in the current API version. MVP: poll
	// DomainGetState at 1Hz for c.Duration and report transitions.
	target := ""
	if c.Vm != "" {
		target = vmDomainNameFor(c.Vm)
	}

	deadline := time.Now().Add(c.Duration)
	prev := map[string]string{}
	for time.Now().Before(deadline) {
		flags := libvirt.ConnectListDomainsActive | libvirt.ConnectListDomainsInactive
		doms, _, err := conn.l.ConnectListAllDomains(1, flags)
		if err != nil {
			return err
		}
		for _, d := range doms {
			if target != "" && d.Name != target {
				continue
			}
			state, _, serr := conn.l.DomainGetState(d, 0)
			if serr != nil {
				continue
			}
			s := domainStateString(libvirt.DomainState(state))
			if prev[d.Name] != s && prev[d.Name] != "" {
				fmt.Printf("%s  %s: %s → %s\n",
					time.Now().Format(time.RFC3339), d.Name, prev[d.Name], s)
			}
			prev[d.Name] = s
		}
		time.Sleep(1 * time.Second)
	}
	return nil
}

// ---------------- guest subgroup ----------------

type LibvirtGuestGroup struct {
	Ping       LibvirtGuestPingCmd       `cmd:"" help:"Ping qemu-guest-agent"`
	Info       LibvirtGuestInfoCmd       `cmd:"" help:"Show agent capabilities"`
	OsInfo     LibvirtGuestOsInfoCmd     `cmd:"" name:"os-info" help:"Guest OS information"`
	Time       LibvirtGuestTimeCmd       `cmd:"" help:"Guest clock time"`
	Hostname   LibvirtGuestHostnameCmd   `cmd:"" help:"Guest hostname"`
	Users      LibvirtGuestUsersCmd      `cmd:"" help:"Logged-in users"`
	Interfaces LibvirtGuestInterfacesCmd `cmd:"" help:"Network interfaces + IPs"`
	Disks      LibvirtGuestDisksCmd      `cmd:"" help:"Guest-visible disks"`
	Fsinfo     LibvirtGuestFsinfoCmd     `cmd:"" name:"fsinfo" help:"Mounted filesystems"`
	Vcpus      LibvirtGuestVcpusCmd      `cmd:"" help:"Guest-side vCPU state"`
	Exec       LibvirtGuestExecCmd       `cmd:"" help:"Run a command via guest-exec"`
	File       LibvirtGuestFileGroup     `cmd:"" help:"Read/write guest files"`
	Fsfreeze   LibvirtGuestFsfreezeGroup `cmd:"" help:"Filesystem freeze control"`
	Fstrim     LibvirtGuestFstrimCmd     `cmd:"" help:"Run TRIM on guest filesystems"`
}

type LibvirtGuestPingCmd struct {
	Vm      string        `arg:"" help:"VM name"`
	Timeout time.Duration `long:"timeout" default:"5s" help:"Max wait"`
	libvirtURIFlag
}

func (c *LibvirtGuestPingCmd) Run() error {
	t, err := ResolveVmTarget(c.Vm, c.Uri)
	if err != nil {
		return err
	}
	defer t.Close() //nolint:errcheck
	a := NewGuestAgent(t.Conn.l, t.Domain, c.Timeout)
	if err := a.Ping(); err != nil {
		return fmt.Errorf("agent ping: %w", err)
	}
	fmt.Fprintln(os.Stderr, "agent responsive")
	return nil
}

type LibvirtGuestInfoCmd struct {
	Vm string `arg:"" help:"VM name"`
	libvirtURIFlag
}

func (c *LibvirtGuestInfoCmd) Run() error {
	t, err := ResolveVmTarget(c.Vm, c.Uri)
	if err != nil {
		return err
	}
	defer t.Close() //nolint:errcheck
	a := NewGuestAgent(t.Conn.l, t.Domain, 10*time.Second)
	info, err := a.Info()
	if err != nil {
		return err
	}
	return writeJSON(os.Stdout, info)
}

type LibvirtGuestOsInfoCmd struct {
	Vm string `arg:"" help:"VM name"`
	libvirtURIFlag
}

func (c *LibvirtGuestOsInfoCmd) Run() error {
	t, err := ResolveVmTarget(c.Vm, c.Uri)
	if err != nil {
		return err
	}
	defer t.Close() //nolint:errcheck
	a := NewGuestAgent(t.Conn.l, t.Domain, 10*time.Second)
	info, err := a.OSInfo()
	if err != nil {
		return err
	}
	return writeJSON(os.Stdout, info)
}

type LibvirtGuestTimeCmd struct {
	Vm string `arg:"" help:"VM name"`
	libvirtURIFlag
}

func (c *LibvirtGuestTimeCmd) Run() error {
	t, err := ResolveVmTarget(c.Vm, c.Uri)
	if err != nil {
		return err
	}
	defer t.Close() //nolint:errcheck
	a := NewGuestAgent(t.Conn.l, t.Domain, 10*time.Second)
	gt, err := a.Time()
	if err != nil {
		return err
	}
	delta := time.Since(gt)
	fmt.Printf("guest_time: %s  delta: %s\n", gt.Format(time.RFC3339), delta.Round(time.Second))
	return nil
}

type LibvirtGuestHostnameCmd struct {
	Vm string `arg:"" help:"VM name"`
	libvirtURIFlag
}

func (c *LibvirtGuestHostnameCmd) Run() error {
	t, err := ResolveVmTarget(c.Vm, c.Uri)
	if err != nil {
		return err
	}
	defer t.Close() //nolint:errcheck
	a := NewGuestAgent(t.Conn.l, t.Domain, 10*time.Second)
	h, err := a.Hostname()
	if err != nil {
		return err
	}
	fmt.Println(h)
	return nil
}

type LibvirtGuestUsersCmd struct {
	Vm string `arg:"" help:"VM name"`
	libvirtURIFlag
}

func (c *LibvirtGuestUsersCmd) Run() error {
	t, err := ResolveVmTarget(c.Vm, c.Uri)
	if err != nil {
		return err
	}
	defer t.Close() //nolint:errcheck
	a := NewGuestAgent(t.Conn.l, t.Domain, 10*time.Second)
	users, err := a.Users()
	if err != nil {
		return err
	}
	return writeJSON(os.Stdout, users)
}

type LibvirtGuestInterfacesCmd struct {
	Vm string `arg:"" help:"VM name"`
	libvirtURIFlag
}

func (c *LibvirtGuestInterfacesCmd) Run() error {
	t, err := ResolveVmTarget(c.Vm, c.Uri)
	if err != nil {
		return err
	}
	defer t.Close() //nolint:errcheck
	a := NewGuestAgent(t.Conn.l, t.Domain, 10*time.Second)
	ifs, err := a.NetworkInterfaces()
	if err != nil {
		return err
	}
	return writeJSON(os.Stdout, ifs)
}

type LibvirtGuestDisksCmd struct {
	Vm string `arg:"" help:"VM name"`
	libvirtURIFlag
}

func (c *LibvirtGuestDisksCmd) Run() error {
	t, err := ResolveVmTarget(c.Vm, c.Uri)
	if err != nil {
		return err
	}
	defer t.Close() //nolint:errcheck
	a := NewGuestAgent(t.Conn.l, t.Domain, 10*time.Second)
	d, err := a.Disks()
	if err != nil {
		return err
	}
	return writeJSON(os.Stdout, d)
}

type LibvirtGuestFsinfoCmd struct {
	Vm string `arg:"" help:"VM name"`
	libvirtURIFlag
}

func (c *LibvirtGuestFsinfoCmd) Run() error {
	t, err := ResolveVmTarget(c.Vm, c.Uri)
	if err != nil {
		return err
	}
	defer t.Close() //nolint:errcheck
	a := NewGuestAgent(t.Conn.l, t.Domain, 10*time.Second)
	fs, err := a.FSInfo()
	if err != nil {
		return err
	}
	return writeJSON(os.Stdout, fs)
}

type LibvirtGuestVcpusCmd struct {
	Vm string `arg:"" help:"VM name"`
	libvirtURIFlag
}

func (c *LibvirtGuestVcpusCmd) Run() error {
	t, err := ResolveVmTarget(c.Vm, c.Uri)
	if err != nil {
		return err
	}
	defer t.Close() //nolint:errcheck
	a := NewGuestAgent(t.Conn.l, t.Domain, 10*time.Second)
	v, err := a.VCPUs()
	if err != nil {
		return err
	}
	return writeJSON(os.Stdout, v)
}

type LibvirtGuestExecCmd struct {
	Vm      string        `arg:"" help:"VM name"`
	Argv    []string      `arg:"" help:"Command and arguments (e.g. 'uname -a')"`
	Capture bool          `long:"capture" default:"true" help:"Capture stdout/stderr"`
	Wait    time.Duration `long:"wait" default:"60s" help:"Max wait for command to complete"`
	libvirtURIFlag
}

func (c *LibvirtGuestExecCmd) Run() error {
	t, err := ResolveVmTarget(c.Vm, c.Uri)
	if err != nil {
		return err
	}
	defer t.Close() //nolint:errcheck
	a := NewGuestAgent(t.Conn.l, t.Domain, 30*time.Second)
	status, err := a.ExecAndWait(c.Argv, c.Capture, 250*time.Millisecond, c.Wait)
	if err != nil {
		return err
	}
	if status.OutData != "" {
		data, _ := base64Decode(status.OutData)
		_, _ = os.Stdout.Write(data)
	}
	if status.ErrData != "" {
		data, _ := base64Decode(status.ErrData)
		_, _ = os.Stderr.Write(data)
	}
	if status.Signal != 0 {
		return fmt.Errorf("killed by signal %d", status.Signal)
	}
	if status.ExitCode != 0 {
		return fmt.Errorf("exit %d", status.ExitCode)
	}
	return nil
}

type LibvirtGuestFileGroup struct {
	Read  LibvirtGuestFileReadCmd  `cmd:"" help:"Read a guest file"`
	Write LibvirtGuestFileWriteCmd `cmd:"" help:"Write a guest file (from stdin)"`
}

type LibvirtGuestFileReadCmd struct {
	Vm   string `arg:"" help:"VM name"`
	Path string `arg:"" help:"Absolute path in guest"`
	libvirtURIFlag
}

func (c *LibvirtGuestFileReadCmd) Run() error {
	t, err := ResolveVmTarget(c.Vm, c.Uri)
	if err != nil {
		return err
	}
	defer t.Close() //nolint:errcheck
	a := NewGuestAgent(t.Conn.l, t.Domain, 30*time.Second)
	data, err := a.FileRead(c.Path)
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(data)
	return err
}

type LibvirtGuestFileWriteCmd struct {
	Vm   string `arg:"" help:"VM name"`
	Path string `arg:"" help:"Absolute path in guest"`
	libvirtURIFlag
}

func (c *LibvirtGuestFileWriteCmd) Run() error {
	t, err := ResolveVmTarget(c.Vm, c.Uri)
	if err != nil {
		return err
	}
	defer t.Close() //nolint:errcheck
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return fmt.Errorf("reading stdin: %w", err)
	}
	a := NewGuestAgent(t.Conn.l, t.Domain, 30*time.Second)
	if err := a.FileWrite(c.Path, data); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "wrote %d bytes to %s in %s\n", len(data), c.Path, c.Vm)
	return nil
}

type LibvirtGuestFsfreezeGroup struct {
	Status LibvirtGuestFsfreezeStatusCmd `cmd:"" help:"Report freeze status"`
	Freeze LibvirtGuestFsfreezeFreezeCmd `cmd:"" help:"Freeze all filesystems"`
	Thaw   LibvirtGuestFsfreezeThawCmd   `cmd:"" help:"Thaw frozen filesystems"`
}

type LibvirtGuestFsfreezeStatusCmd struct {
	Vm string `arg:"" help:"VM name"`
	libvirtURIFlag
}

func (c *LibvirtGuestFsfreezeStatusCmd) Run() error {
	t, err := ResolveVmTarget(c.Vm, c.Uri)
	if err != nil {
		return err
	}
	defer t.Close() //nolint:errcheck
	a := NewGuestAgent(t.Conn.l, t.Domain, 10*time.Second)
	s, err := a.FsFreezeStatus()
	if err != nil {
		return err
	}
	fmt.Println(s)
	return nil
}

type LibvirtGuestFsfreezeFreezeCmd struct {
	Vm string `arg:"" help:"VM name"`
	libvirtURIFlag
}

func (c *LibvirtGuestFsfreezeFreezeCmd) Run() error {
	t, err := ResolveVmTarget(c.Vm, c.Uri)
	if err != nil {
		return err
	}
	defer t.Close() //nolint:errcheck
	a := NewGuestAgent(t.Conn.l, t.Domain, 30*time.Second)
	n, err := a.FsFreeze()
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "froze %d filesystems\n", n)
	return nil
}

type LibvirtGuestFsfreezeThawCmd struct {
	Vm string `arg:"" help:"VM name"`
	libvirtURIFlag
}

func (c *LibvirtGuestFsfreezeThawCmd) Run() error {
	t, err := ResolveVmTarget(c.Vm, c.Uri)
	if err != nil {
		return err
	}
	defer t.Close() //nolint:errcheck
	a := NewGuestAgent(t.Conn.l, t.Domain, 30*time.Second)
	n, err := a.FsThaw()
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "thawed %d filesystems\n", n)
	return nil
}

type LibvirtGuestFstrimCmd struct {
	Vm      string `arg:"" help:"VM name"`
	Minimum int    `long:"minimum" default:"0" help:"Minimum extent size in bytes"`
	libvirtURIFlag
}

func (c *LibvirtGuestFstrimCmd) Run() error {
	t, err := ResolveVmTarget(c.Vm, c.Uri)
	if err != nil {
		return err
	}
	defer t.Close() //nolint:errcheck
	a := NewGuestAgent(t.Conn.l, t.Domain, 60*time.Second)
	if err := a.FsTrim(uint64(c.Minimum)); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "fstrim completed")
	return nil
}

// ---------------- snapshot subgroup ----------------

type LibvirtSnapshotGroup struct {
	List   LibvirtSnapshotListCmd   `cmd:"" help:"List snapshots"`
	Create LibvirtSnapshotCreateCmd `cmd:"" help:"Create a snapshot"`
	Info   LibvirtSnapshotInfoCmd   `cmd:"" help:"Show snapshot XML"`
	Revert LibvirtSnapshotRevertCmd `cmd:"" help:"Revert to a snapshot"`
	Delete LibvirtSnapshotDeleteCmd `cmd:"" help:"Delete a snapshot"`
}

type LibvirtSnapshotListCmd struct {
	Vm string `arg:"" help:"VM name"`
	libvirtURIFlag
}

func (c *LibvirtSnapshotListCmd) Run() error {
	t, err := ResolveVmTarget(c.Vm, c.Uri)
	if err != nil {
		return err
	}
	defer t.Close() //nolint:errcheck
	snaps, _, err := t.Conn.l.DomainListAllSnapshots(t.Domain, 1, 0)
	if err != nil {
		return err
	}
	for _, s := range snaps {
		fmt.Println(s.Name)
	}
	_ = context.TODO()
	return nil
}

type LibvirtSnapshotCreateCmd struct {
	Vm       string `arg:"" help:"VM name"`
	Name     string `arg:"" help:"Snapshot name"`
	Desc     string `long:"desc" help:"Description"`
	DiskOnly bool   `long:"disk-only" help:"Disk-only snapshot (skip memory)"`
	libvirtURIFlag
}

func (c *LibvirtSnapshotCreateCmd) Run() error {
	t, err := ResolveVmTarget(c.Vm, c.Uri)
	if err != nil {
		return err
	}
	defer t.Close() //nolint:errcheck
	snap := libvirtxml.DomainSnapshot{
		Name:        c.Name,
		Description: c.Desc,
	}
	xmlStr, err := xml.Marshal(&snap)
	if err != nil {
		return err
	}
	var flags uint32
	if c.DiskOnly {
		flags |= uint32(libvirt.DomainSnapshotCreateDiskOnly)
	}
	_, err = t.Conn.l.DomainSnapshotCreateXML(t.Domain, string(xmlStr), flags)
	if err != nil {
		return fmt.Errorf("creating snapshot: %w", err)
	}
	fmt.Fprintf(os.Stderr, "snapshot %s created for %s\n", c.Name, t.DomName)
	return nil
}

type LibvirtSnapshotInfoCmd struct {
	Vm   string `arg:"" help:"VM name"`
	Name string `arg:"" help:"Snapshot name"`
	libvirtURIFlag
}

func (c *LibvirtSnapshotInfoCmd) Run() error {
	t, err := ResolveVmTarget(c.Vm, c.Uri)
	if err != nil {
		return err
	}
	defer t.Close() //nolint:errcheck
	snap, err := t.Conn.l.DomainSnapshotLookupByName(t.Domain, c.Name, 0)
	if err != nil {
		return err
	}
	xmlStr, err := t.Conn.l.DomainSnapshotGetXMLDesc(snap, 0)
	if err != nil {
		return err
	}
	fmt.Print(xmlStr)
	return nil
}

type LibvirtSnapshotRevertCmd struct {
	Vm   string `arg:"" help:"VM name"`
	Name string `arg:"" help:"Snapshot name"`
	libvirtURIFlag
}

func (c *LibvirtSnapshotRevertCmd) Run() error {
	t, err := ResolveVmTarget(c.Vm, c.Uri)
	if err != nil {
		return err
	}
	defer t.Close() //nolint:errcheck
	snap, err := t.Conn.l.DomainSnapshotLookupByName(t.Domain, c.Name, 0)
	if err != nil {
		return err
	}
	if err := t.Conn.l.DomainRevertToSnapshot(snap, 0); err != nil {
		return fmt.Errorf("revert: %w", err)
	}
	fmt.Fprintf(os.Stderr, "reverted %s to snapshot %s\n", t.DomName, c.Name)
	return nil
}

type LibvirtSnapshotDeleteCmd struct {
	Vm   string `arg:"" help:"VM name"`
	Name string `arg:"" help:"Snapshot name"`
	libvirtURIFlag
}

func (c *LibvirtSnapshotDeleteCmd) Run() error {
	t, err := ResolveVmTarget(c.Vm, c.Uri)
	if err != nil {
		return err
	}
	defer t.Close() //nolint:errcheck
	snap, err := t.Conn.l.DomainSnapshotLookupByName(t.Domain, c.Name, 0)
	if err != nil {
		return err
	}
	if err := t.Conn.l.DomainSnapshotDelete(snap, 0); err != nil {
		return fmt.Errorf("delete: %w", err)
	}
	fmt.Fprintf(os.Stderr, "deleted snapshot %s of %s\n", c.Name, t.DomName)
	return nil
}
