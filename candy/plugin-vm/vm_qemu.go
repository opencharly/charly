package vm

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	govmmQemu "github.com/kata-containers/govmm/qemu"
)

// qemuAlive reports whether the QEMU process recorded in <stateDir>/qemu.pid is
// still running. A pidfile alone is not liveness: a dead PID, or a reused PID
// that now belongs to an unrelated process, must report false — the cmdline check
// closes the reused-PID false-positive (a Signal(0) probe alone would report a
// recycled PID as "running"). Shared by `vm start` (the idempotent already-running
// guard) and `vm list` (the qemu state scan) — R3, one liveness probe.
func qemuAlive(stateDir string) bool {
	pidFile := filepath.Join(stateDir, "qemu.pid")
	data, err := os.ReadFile(pidFile)
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		return false
	}
	cmdline, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		return false
	}
	return bytes.Contains(cmdline, []byte("qemu-system"))
}

// qemuGracefulShutdown sends a system_powerdown command via QMP for ACPI shutdown.
func qemuGracefulShutdown(stateDir string) error {
	qmpSocket := filepath.Join(stateDir, "qmp.sock")

	if _, err := os.Stat(qmpSocket); err != nil {
		return fmt.Errorf("QMP socket not found at %s", qmpSocket)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	disconnectedCh := make(chan struct{})
	qmp, _, err := govmmQemu.QMPStart(ctx, qmpSocket, govmmQemu.QMPConfig{}, disconnectedCh)
	if err != nil {
		return fmt.Errorf("connecting to QMP: %w", err)
	}
	defer qmp.Shutdown()

	if err := qmp.ExecuteQMPCapabilities(ctx); err != nil {
		return fmt.Errorf("QMP capabilities: %w", err)
	}

	if err := qmp.ExecuteSystemPowerdown(ctx); err != nil {
		return fmt.Errorf("QMP system_powerdown: %w", err)
	}

	return nil
}

// qemuForceShutdown sends a quit command via QMP to force QEMU to exit immediately.
func qemuForceShutdown(stateDir string) error {
	qmpSocket := filepath.Join(stateDir, "qmp.sock")

	if _, err := os.Stat(qmpSocket); err != nil {
		return fmt.Errorf("QMP socket not found at %s", qmpSocket)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	disconnectedCh := make(chan struct{})
	qmp, _, err := govmmQemu.QMPStart(ctx, qmpSocket, govmmQemu.QMPConfig{}, disconnectedCh)
	if err != nil {
		return fmt.Errorf("connecting to QMP: %w", err)
	}
	defer qmp.Shutdown()

	if err := qmp.ExecuteQMPCapabilities(ctx); err != nil {
		return fmt.Errorf("QMP capabilities: %w", err)
	}

	if err := qmp.ExecuteQuit(ctx); err != nil {
		return fmt.Errorf("QMP quit: %w", err)
	}

	return nil
}
