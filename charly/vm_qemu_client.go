package main

import "fmt"

// vm_qemu_client.go — host-side RPC wrappers for the direct-QEMU QMP shutdown ops. The govmm QMP
// impl (formerly vm_qemu.go's qemuGracefulShutdown/qemuForceShutdown) moved to candy/plugin-vm;
// vm.go's qemu-backend lifecycle branches call these wrappers, which RPC the out-of-process plugin.
// killQemuByPID stays pure-host in vm_host_helpers.go (an OS process kill, no govmm).

func qemuGracefulShutdown(stateDir string) error {
	return vmQemuShutdown(stateDir, false)
}

func qemuForceShutdown(stateDir string) error {
	return vmQemuShutdown(stateDir, true)
}

func vmQemuShutdown(stateDir string, force bool) error {
	raw, ok := invokeVmPluginEnv(vmPluginEnv{VmOp: "qemu-shutdown", StateDir: stateDir, Force: force})
	if !ok {
		return fmt.Errorf("vm plugin unavailable (govmm QMP is out-of-process)")
	}
	if e := vmPluginOpError(raw); e != "" {
		return fmt.Errorf("%s", e)
	}
	return nil
}
