package main

import (
	"context"

	"github.com/opencharly/sdk/spec"
)

// host_build_vm_build.go — the "vm-build" F10 host-builder. The `charly vm build` command moved to
// command:vm (candy/plugin-vm, P10), but the VM-disk build ENGINE it drives (RunPrivileged pacstrap/
// bootc, BuildCloudImage/BuildBootcVM/BuildBootstrapVM, LoadBuildConfigForBox — the privileged-runner +
// box-store Mechanisms) STAYS CORE, exactly as the box-build engine stayed core behind HostBuild("image")
// in P8. So core keeps the whole VmBuildCmd orchestration; the plugin's thin `vm build` command forwards
// its flags via HostBuild("vm-build") and this builder runs the full build in-process (the plugin is
// compiled-in, so build progress flows to the shared stdout/stderr). Generic action noun (F11).
const vmBuildBuilderKind = "vm-build"

func hostBuildVmBuild(_ context.Context, req spec.VmBuildRequest, _ buildEngineContext) (spec.VmBuildReply, error) {
	cmd := VmBuildCmd{
		Box:       req.Box,
		Size:      req.Size,
		RootSize:  req.RootSize,
		Tag:       req.Tag,
		Type:      req.Type,
		Transport: req.Transport,
		Console:   req.Console,
	}
	if cmd.Type == "" {
		cmd.Type = "qcow2" // the former Kong `default:"qcow2"` — the plugin's thin command defaults it too
	}
	return spec.VmBuildReply{}, cmd.Run()
}

var _ = func() bool {
	registerHostBuilder(vmBuildBuilderKind, typedHostBuilder(vmBuildBuilderKind, hostBuildVmBuild))
	return true
}()
