package vm

import (
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk/spec"
)

// vm_build.go — the THIN command:vm `charly vm build`. The VM-disk build ENGINE (privileged pacstrap/
// bootc/cloud-image → qcow2) STAYS CORE (P8-consistent — the box-build engine stayed core behind
// HostBuild too); this command only parses flags and forwards them to the host over HostBuild("vm-build"),
// which runs the whole build in-process (command:vm is compiled-in, so progress flows to the shared stdio).
type VmBuildCmd struct {
	Box       string `arg:"" help:"Bootc image name"`
	Size      string `long:"size" help:"Override disk size (e.g. 20G, '20 GiB')"`
	RootSize  string `long:"root-size" help:"Override root partition size (e.g. 10G)"`
	Tag       string `long:"tag" help:"Image tag override"`
	Type      string `long:"type" default:"qcow2" help:"Output format: qcow2, raw"`
	Transport string `long:"transport" help:"Image transport: registry, containers-storage, oci, oci-archive"`
	Console   bool   `long:"console" help:"Enable console output for debugging"`
}

func (c *VmBuildCmd) Run() error {
	if cmdExec == nil {
		return fmt.Errorf("vm build: no host reverse channel (command not compiled-in?)")
	}
	reqJSON, err := json.Marshal(spec.VmBuildRequest{
		Box: c.Box, Size: c.Size, RootSize: c.RootSize, Tag: c.Tag,
		Type: c.Type, Transport: c.Transport, Console: c.Console,
	})
	if err != nil {
		return err
	}
	_, err = cmdExec.HostBuild(cmdCtx, "vm-build", reqJSON)
	return err
}
