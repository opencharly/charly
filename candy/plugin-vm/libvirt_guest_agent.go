package main

// Typed client for the qemu-guest-agent protocol via go-libvirt's
// QEMUDomainAgentCommand. Each method sends a JSON command string to
// the agent daemon running inside the guest (via the spicevmc channel
// or direct virtio-serial) and unmarshals the JSON reply.
//
// Used by `charly check libvirt guest <verb>`.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	libvirt "github.com/digitalocean/go-libvirt"
)

// GuestAgent is a thin typed layer over libvirt's agent command RPC.
type GuestAgent struct {
	l  *libvirt.Libvirt
	d  libvirt.Domain
	to int32 // default timeout in seconds
}

// NewGuestAgent wraps an open libvirt connection + domain.
func NewGuestAgent(l *libvirt.Libvirt, d libvirt.Domain, defaultTimeout time.Duration) *GuestAgent {
	t := int32(defaultTimeout.Seconds())
	if t <= 0 {
		t = 10
	}
	return &GuestAgent{l: l, d: d, to: t}
}

// Call sends a JSON-RPC-style command and unmarshals the reply into
// `out` (which may be nil to ignore). `args` may be nil if the command
// takes no arguments.
func (a *GuestAgent) Call(cmd string, args any, out any) error {
	req := map[string]any{"execute": cmd}
	if args != nil {
		req["arguments"] = args
	}
	buf, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshaling request: %w", err)
	}
	rep, err := a.l.QEMUDomainAgentCommand(a.d, string(buf), a.to, 0)
	if err != nil {
		return fmt.Errorf("agent call %s: %w", cmd, err)
	}
	if out == nil {
		return nil
	}
	// Reply shape: {"return": <any>}
	var envelope struct {
		Return json.RawMessage `json:"return"`
		Error  *struct {
			Class string `json:"class"`
			Desc  string `json:"desc"`
		} `json:"error,omitempty"`
	}
	if len(rep) == 0 {
		return fmt.Errorf("empty agent reply")
	}
	s := rep[0]
	if err := json.Unmarshal([]byte(s), &envelope); err != nil {
		return fmt.Errorf("unmarshal reply: %w (raw: %s)", err, s)
	}
	if envelope.Error != nil {
		return fmt.Errorf("agent error %s: %s", envelope.Error.Class, envelope.Error.Desc)
	}
	if len(envelope.Return) > 0 {
		if err := json.Unmarshal(envelope.Return, out); err != nil {
			return fmt.Errorf("unmarshal return: %w", err)
		}
	}
	return nil
}

// Ping verifies the agent is responsive.
func (a *GuestAgent) Ping() error {
	return a.Call("guest-ping", nil, nil)
}

// Info returns the agent's supported command list.
type GuestAgentInfo struct {
	Version           string                       `json:"version"`
	SupportedCommands []GuestAgentSupportedCommand `json:"supported_commands"`
}

type GuestAgentSupportedCommand struct {
	Name            string `json:"name"`
	Enabled         bool   `json:"enabled"`
	SuccessResponse bool   `json:"success-response"`
}

func (a *GuestAgent) Info() (*GuestAgentInfo, error) {
	var out GuestAgentInfo
	if err := a.Call("guest-info", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// OSInfo returns guest OS identification.
type GuestOSInfo struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	PrettyName    string `json:"pretty-name"`
	Version       string `json:"version"`
	VersionID     string `json:"version-id"`
	KernelRelease string `json:"kernel-release"`
	KernelVersion string `json:"kernel-version"`
	Machine       string `json:"machine"`
	Variant       string `json:"variant,omitempty"`
	VariantID     string `json:"variant-id,omitempty"`
}

func (a *GuestAgent) OSInfo() (*GuestOSInfo, error) {
	var out GuestOSInfo
	if err := a.Call("guest-get-osinfo", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Hostname returns the guest's hostname.
func (a *GuestAgent) Hostname() (string, error) {
	var out struct {
		HostName string `json:"host-name"`
	}
	if err := a.Call("guest-get-host-name", nil, &out); err != nil {
		return "", err
	}
	return out.HostName, nil
}

// Time returns the guest's clock in nanoseconds since UNIX epoch.
func (a *GuestAgent) Time() (time.Time, error) {
	var out int64
	if err := a.Call("guest-get-time", nil, &out); err != nil {
		return time.Time{}, err
	}
	return time.Unix(0, out), nil
}

// Users returns the list of logged-in users.
type GuestUser struct {
	User      string  `json:"user"`
	Domain    string  `json:"domain,omitempty"`
	LoginTime float64 `json:"login-time"`
}

func (a *GuestAgent) Users() ([]GuestUser, error) {
	var out []GuestUser
	if err := a.Call("guest-get-users", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// NetworkInterfaces returns the guest's network interfaces.
type GuestNetworkInterface struct {
	Name            string                 `json:"name"`
	HardwareAddress string                 `json:"hardware-address"`
	IPAddresses     []GuestNetworkIP       `json:"ip-addresses"`
	Statistics      *GuestNetworkStatistic `json:"statistics,omitempty"`
}

type GuestNetworkIP struct {
	Type    string `json:"ip-address-type"`
	Address string `json:"ip-address"`
	Prefix  uint   `json:"prefix"`
}

type GuestNetworkStatistic struct {
	RxBytes   uint64 `json:"rx-bytes"`
	RxPackets uint64 `json:"rx-packets"`
	RxErrs    uint64 `json:"rx-errs"`
	RxDropped uint64 `json:"rx-dropped"`
	TxBytes   uint64 `json:"tx-bytes"`
	TxPackets uint64 `json:"tx-packets"`
	TxErrs    uint64 `json:"tx-errs"`
	TxDropped uint64 `json:"tx-dropped"`
}

func (a *GuestAgent) NetworkInterfaces() ([]GuestNetworkInterface, error) {
	var out []GuestNetworkInterface
	if err := a.Call("guest-network-get-interfaces", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Disks returns the guest's block devices (QGA >= 5.2).
type GuestDisk struct {
	Name       string   `json:"name"`
	Partition  bool     `json:"partition,omitempty"`
	Alias      string   `json:"alias,omitempty"`
	BusType    string   `json:"bus-type,omitempty"`
	Serial     string   `json:"serial,omitempty"`
	Dependents []string `json:"dependents,omitempty"`
}

func (a *GuestAgent) Disks() ([]GuestDisk, error) {
	var out []GuestDisk
	if err := a.Call("guest-get-disks", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// FSInfo returns mounted filesystem info.
type GuestFS struct {
	Name       string           `json:"name"`
	Mountpoint string           `json:"mountpoint"`
	Type       string           `json:"type"`
	UsedBytes  uint64           `json:"used-bytes,omitempty"`
	TotalBytes uint64           `json:"total-bytes,omitempty"`
	Disk       []map[string]any `json:"disk,omitempty"`
}

func (a *GuestAgent) FSInfo() ([]GuestFS, error) {
	var out []GuestFS
	if err := a.Call("guest-get-fsinfo", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// VCPUs returns guest-side vCPU online state.
type GuestVCPU struct {
	LogicalID  uint `json:"logical-id"`
	Online     bool `json:"online"`
	CanOffline bool `json:"can-offline"`
}

func (a *GuestAgent) VCPUs() ([]GuestVCPU, error) {
	var out []GuestVCPU
	if err := a.Call("guest-get-vcpus", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// MemoryBlockInfo returns guest memory hotplug state.
type GuestMemoryBlockInfo struct {
	Size uint64 `json:"size"`
}

func (a *GuestAgent) MemoryBlockInfo() (*GuestMemoryBlockInfo, error) {
	var out GuestMemoryBlockInfo
	if err := a.Call("guest-get-memory-block-info", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Exec runs a command in the guest. `capture` is whether to capture
// stdout/stderr. Returns the PID immediately; use ExecStatus to poll.
type GuestExecRequest struct {
	Path          string   `json:"path"`
	Arg           []string `json:"arg,omitempty"`
	Env           []string `json:"env,omitempty"`
	InputData     string   `json:"input-data,omitempty"`
	CaptureOutput bool     `json:"capture-output,omitempty"`
}

func (a *GuestAgent) Exec(req GuestExecRequest) (int, error) {
	var out struct {
		PID int `json:"pid"`
	}
	if err := a.Call("guest-exec", req, &out); err != nil {
		return 0, err
	}
	return out.PID, nil
}

// ExecStatus polls an exec result. `Exited` indicates whether the
// command finished.
type GuestExecStatus struct {
	Exited       bool   `json:"exited"`
	ExitCode     int    `json:"exitcode,omitempty"`
	Signal       int    `json:"signal,omitempty"`
	OutData      string `json:"out-data,omitempty"`
	ErrData      string `json:"err-data,omitempty"`
	OutTruncated bool   `json:"out-truncated,omitempty"`
	ErrTruncated bool   `json:"err-truncated,omitempty"`
}

func (a *GuestAgent) ExecStatus(pid int) (*GuestExecStatus, error) {
	var out GuestExecStatus
	if err := a.Call("guest-exec-status", map[string]int{"pid": pid}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ExecAndWait runs a command and polls until it completes or ctx expires.
func (a *GuestAgent) ExecAndWait(argv []string, capture bool, pollInterval time.Duration, maxWait time.Duration) (*GuestExecStatus, error) {
	if len(argv) == 0 {
		return nil, fmt.Errorf("empty argv")
	}
	req := GuestExecRequest{
		Path:          argv[0],
		Arg:           argv[1:],
		CaptureOutput: capture,
	}
	pid, err := a.Exec(req)
	if err != nil {
		return nil, err
	}
	if pollInterval <= 0 {
		pollInterval = 250 * time.Millisecond
	}
	if maxWait <= 0 {
		maxWait = 60 * time.Second
	}
	// AUTHOR/CALLER cap (poll.go WaitCapped, NoProgress disabled): maxWait + the
	// caller's pollInterval are the contract, preserved exactly. The bespoke
	// deadline loop is replaced by the unified primitive (R3).
	var result *GuestExecStatus
	var statusErr error
	cfg := loadedReadiness().WaitCapped(fmt.Sprintf("guest-exec pid=%d", pid), PollRemote, maxWait)
	cfg.Interval = pollInterval
	pErr := pollUntil(context.Background(), cfg, func(context.Context) (bool, float64, error) {
		status, serr := a.ExecStatus(pid)
		if serr != nil {
			statusErr = serr
			return false, 0, ErrPollFatal // ExecStatus error → abort now (as today)
		}
		if status.Exited {
			result = status
			return true, 0, nil
		}
		return false, 0, nil
	})
	if statusErr != nil {
		return nil, statusErr
	}
	if pErr != nil {
		return nil, fmt.Errorf("guest-exec pid=%d did not complete within %s", pid, maxWait)
	}
	return result, nil
}

// FileRead reads a guest file via guest-file-open / guest-file-read /
// guest-file-close. Returns the concatenated contents (base64-decoded).
func (a *GuestAgent) FileRead(path string) ([]byte, error) {
	var openResp int
	if err := a.Call("guest-file-open", map[string]string{"path": path, "mode": "r"}, &openResp); err != nil {
		return nil, err
	}
	handle := openResp
	defer func() { _ = a.Call("guest-file-close", map[string]int{"handle": handle}, nil) }()

	var all []byte
	for {
		var resp struct {
			Count  int    `json:"count"`
			BufB64 string `json:"buf-b64"`
			EOF    bool   `json:"eof"`
		}
		if err := a.Call("guest-file-read", map[string]any{
			"handle": handle,
			"count":  48 * 1024,
		}, &resp); err != nil {
			return nil, err
		}
		if resp.Count == 0 && resp.EOF {
			break
		}
		if resp.BufB64 != "" {
			buf, err := base64Decode(resp.BufB64)
			if err != nil {
				return nil, fmt.Errorf("decoding file data: %w", err)
			}
			all = append(all, buf...)
		}
		if resp.EOF {
			break
		}
	}
	return all, nil
}

// FileWrite writes `data` to a guest file (truncating). Uses
// guest-file-open / guest-file-write / guest-file-close.
func (a *GuestAgent) FileWrite(path string, data []byte) error {
	var openResp int
	if err := a.Call("guest-file-open", map[string]string{"path": path, "mode": "w"}, &openResp); err != nil {
		return err
	}
	handle := openResp
	defer func() { _ = a.Call("guest-file-close", map[string]int{"handle": handle}, nil) }()

	b64 := base64Encode(data)
	var resp struct {
		Count int  `json:"count"`
		EOF   bool `json:"eof"`
	}
	if err := a.Call("guest-file-write", map[string]any{
		"handle":  handle,
		"buf-b64": b64,
	}, &resp); err != nil {
		return err
	}
	return nil
}

// FsFreezeStatus returns "thawed" or "frozen".
func (a *GuestAgent) FsFreezeStatus() (string, error) {
	var out string
	if err := a.Call("guest-fsfreeze-status", nil, &out); err != nil {
		return "", err
	}
	return out, nil
}

// FsFreeze freezes all guest filesystems (transactional snapshots).
// Returns the number of frozen filesystems.
func (a *GuestAgent) FsFreeze() (int, error) {
	var n int
	if err := a.Call("guest-fsfreeze-freeze", nil, &n); err != nil {
		return 0, err
	}
	return n, nil
}

// FsThaw un-freezes.
func (a *GuestAgent) FsThaw() (int, error) {
	var n int
	if err := a.Call("guest-fsfreeze-thaw", nil, &n); err != nil {
		return 0, err
	}
	return n, nil
}

// FsTrim issues TRIM/discard on all mounts.
func (a *GuestAgent) FsTrim(minimumBytes uint64) error {
	args := map[string]any{}
	if minimumBytes > 0 {
		args["minimum"] = minimumBytes
	}
	return a.Call("guest-fstrim", args, nil)
}

// base64 helpers over stdlib.

func base64Encode(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

func base64Decode(s string) ([]byte, error) { return base64.StdEncoding.DecodeString(s) }
