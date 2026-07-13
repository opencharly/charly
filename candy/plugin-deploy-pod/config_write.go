package deploypod

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/opencharly/sdk/deploykit"
	pb "github.com/opencharly/sdk/proto"
	"github.com/opencharly/sdk/spec"
)

// config_write.go — the POD config-WRITE Op (P11, Q1=(a)). Under Ruling C the config-WRITE (the
// quadlet/.pod/sidecar/tunnel file generation) is the deploy:pod plugin's; the RESOLVE + host
// side-effects (secret provisioning, saveDeployState, enc-mount, data-seed, systemctl) stay in the
// HOST `charly config` command. So `charly config` resolves the full QuadletConfig + computes the
// exact target PATHS (its unchanged core filename helpers) + provisions the dirs, then Invokes this
// Op with a spec.PodConfigWriteRequest. The plugin renders the file CONTENTS via the deploykit
// generators and os.WriteFiles them at the exact modes the former core write phase used — .container/
// .pod/sidecar 0600, tunnel .service 0644 — so the output is byte-identical. The host owns the write
// CONDITIONALS: an optional path being SET is the signal to write that file kind (PodPath/SidecarPaths
// ⇒ sidecars configured; TunnelPath ⇒ cloudflare tunnel). Same-host (compiled-in) direct write — no
// venue executor (config-write is the pre-deploy config step, not a deploy Op).
func podConfigWrite(req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	var r spec.PodConfigWriteRequest
	if err := json.Unmarshal(req.GetParamsJson(), &r); err != nil {
		return nil, fmt.Errorf("plugin-deploy-pod config-write: decode request: %w", err)
	}
	var cfg deploykit.QuadletConfig
	if err := json.Unmarshal(r.PodConfigJSON, &cfg); err != nil {
		return nil, fmt.Errorf("plugin-deploy-pod config-write: decode QuadletConfig: %w", err)
	}

	var written []string

	// The .container quadlet (always).
	if err := os.WriteFile(r.ContainerPath, []byte(deploykit.GenerateQuadlet(cfg)), 0o600); err != nil {
		return nil, fmt.Errorf("writing quadlet file: %w", err)
	}
	written = append(written, r.ContainerPath)

	// The .pod + sidecar .container files (host set PodPath/SidecarPaths iff sidecars are configured).
	if r.PodPath != "" {
		if err := os.WriteFile(r.PodPath, []byte(deploykit.GeneratePodQuadlet(cfg)), 0o600); err != nil {
			return nil, fmt.Errorf("writing pod file: %w", err)
		}
		written = append(written, r.PodPath)
	}
	for _, sc := range cfg.Sidecar {
		p, ok := r.SidecarPaths[sc.Name]
		if !ok {
			continue
		}
		if err := os.WriteFile(p, []byte(deploykit.GenerateSidecarQuadlet(sc, cfg.PodName)), 0o600); err != nil {
			return nil, fmt.Errorf("writing sidecar file for %s: %w", sc.Name, err)
		}
		written = append(written, p)
	}

	// The cloudflare tunnel companion .service (host set TunnelPath iff cloudflare tunnel configured).
	if r.TunnelPath != "" {
		if err := os.WriteFile(r.TunnelPath, []byte(deploykit.GenerateTunnelUnit(cfg, r.CloudflaredCfgPath)), 0o644); err != nil {
			return nil, fmt.Errorf("writing tunnel service file: %w", err)
		}
		written = append(written, r.TunnelPath)
	}

	return marshalReply(spec.PodConfigWriteReply{WrittenPaths: written})
}
