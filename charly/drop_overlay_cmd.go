package main

// drop_overlay_cmd.go — the hidden `charly __drop-overlay <name>` command (M4). The externalized
// pod lifecycle plugin (candy/plugin-deploy-pod), running out-of-process, cannot call the in-core
// removeDeployOverlayImages (host podman images/rmi) directly, so its PostTeardown forwards here via
// the "cli" host-builder: `charly __drop-overlay <name>` drops the deploy's synthesized
// <name>-overlay images (the `bundle del` extra over `charly remove`, keep-image-gated by the plugin
// before it forwards). Resolves the container engine from the deploy node (default podman).
type DropOverlayCmd struct {
	Name string `arg:"" help:"pod deploy name whose <name>-overlay images to drop"`
}

func (c *DropOverlayCmd) Run() error {
	engine := "podman"
	if node, ok := loadDeployConfigForRead("__drop-overlay").LookupKey(c.Name); ok {
		engine = podDeployEngine(&node)
	}
	removeDeployOverlayImages(engine, c.Name)
	return nil
}
