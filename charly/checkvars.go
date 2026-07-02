package main

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// ContainerInspection is the subset of `podman inspect` JSON that the test
// runner needs to resolve runtime ${…} variables. Field names mirror the
// podman/docker inspect schema — we read the JSON the engine produces, we
// do not construct it.
type ContainerInspection struct {
	Name            string            `json:"Name"`
	Config          InspectConfig     `json:"Config"`
	HostConfig      InspectHostConfig `json:"HostConfig"`
	NetworkSettings InspectNetwork    `json:"NetworkSettings"`
	Mounts          []InspectMount    `json:"Mounts"`
}

// InspectHostConfig carries the fields inside the "HostConfig" object that
// we need — currently just NetworkMode so host-networked containers can be
// detected (their NetworkSettings.Ports is empty, but container ports are
// bound 1:1 to host ports).
type InspectHostConfig struct {
	NetworkMode string `json:"NetworkMode"`
}

// IsHostNetworked returns true when the container uses --network=host, in
// which case every container port is trivially the same host port.
func (c *ContainerInspection) IsHostNetworked() bool {
	if c == nil {
		return false
	}
	return c.HostConfig.NetworkMode == "host"
}

// InspectConfig carries the fields inside the "Config" object that we need:
// the hostname (container's internal name) and the effective env.
type InspectConfig struct {
	Hostname string   `json:"Hostname"`
	Env      []string `json:"Env"`
}

// InspectNetwork carries IP address and port-binding data. Both top-level
// IPAddress and per-network IPAddress are captured — podman rootless with
// the default network uses the per-network form; docker bridge uses the
// top-level form.
type InspectNetwork struct {
	IPAddress string                        `json:"IPAddress"`
	Networks  map[string]InspectNetworkBind `json:"Networks"`
	// Ports keys are like "6379/tcp"; values are nil when unexposed or
	// a slice of bindings when published.
	Ports map[string][]InspectPortBind `json:"Ports"`
}

// InspectNetworkBind is the per-network record under NetworkSettings.Networks.
type InspectNetworkBind struct {
	IPAddress string `json:"IPAddress"`
}

// InspectPortBind is the host-side record of a port publication.
type InspectPortBind struct {
	HostIp   string `json:"HostIp"`
	HostPort string `json:"HostPort"`
}

// InspectMount describes a single mount attached to the container. For named
// volumes Name is set and Source is the volume's _data path; for bind mounts
// Name is empty and Source is the host directory.
type InspectMount struct {
	Type        string `json:"Type"`
	Name        string `json:"Name,omitempty"`
	Source      string `json:"Source"`
	Destination string `json:"Destination"`
}

// InspectContainer is swappable for tests. Real calls shell out to
// `<engine> inspect <name>` (which returns a one-element JSON array).
var InspectContainer = defaultInspectContainer

func defaultInspectContainer(engine, containerName string) (*ContainerInspection, error) {
	binary := EngineBinary(engine)
	cmd := exec.Command(binary, "inspect", containerName)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("inspecting container %s: %w", containerName, err)
	}
	var arr []ContainerInspection
	if err := json.Unmarshal(out, &arr); err != nil {
		return nil, fmt.Errorf("parsing inspect output for %s: %w", containerName, err)
	}
	if len(arr) == 0 {
		return nil, fmt.Errorf("inspect returned no objects for %s", containerName)
	}
	return &arr[0], nil
}

// CheckVarResolver holds the materials for variable expansion under `charly check live`
// (running container present) and `charly check box` (build-time, no container).
// Builders for each scope are ResolveCheckVarsRuntime and ResolveCheckVarsBuild.
type CheckVarResolver struct {
	// Env is the final flat map: plain refs use the name as key, parameterized
	// refs use "name:arg" as key (e.g., "HOST_PORT:6379"). Matches the format
	// expected by ExpandTestVars.
	Env map[string]string
	// HasRuntime is true when the resolver was populated from a running
	// container. When false, runtime-only vars return unresolved (skip path).
	HasRuntime bool
}

// ResolveCheckVarsBuild builds a variable map for build-time tests (no running
// container). Only BoxMetadata-derived vars are populated.
func ResolveCheckVarsBuild(meta *BoxMetadata) *CheckVarResolver {
	env := buildTimeVars(meta, "" /* no instance at build time */)
	return &CheckVarResolver{Env: env, HasRuntime: false}
}

// ResolveCheckVarsRuntime builds a variable map for `charly check live` against a
// running container. It combines image metadata (build-time knowledge),
// deploy overlay (per-host knowledge), and podman inspect (effective runtime
// state). Any of the inputs may be nil.
//
// On inspect failure the function still returns a resolver with the
// build-time portion populated; HasRuntime is false and runtime-only vars
// will be unresolved downstream.
func ResolveCheckVarsRuntime(meta *BoxMetadata, deploy *BundleNode, engine, deployName, containerName, instance string) (*CheckVarResolver, error) {
	_ = deploy // reserved for future per-deploy overrides; instance now arrives explicitly
	env := buildTimeVars(meta, instance)

	// DEPLOY_NAME — the sanitized name of the deployment under check, the same
	// identifier K3sPostProvision uses for the kubeconfig context + ClusterProfile
	// (sanitizeDeployName collapses ':'/'.'/'/'-> '-'). Deploy-scope checks address
	// their own cluster generically via cluster: "${DEPLOY_NAME}". Runtime-only
	// (see runtimeOnlyVarPrefixes) so it is never offered to build-scope checks.
	if deployName != "" {
		env["DEPLOY_NAME"] = sanitizeDeployName(deployName)
	}

	inspection, err := InspectContainer(engine, containerName)
	if err != nil {
		return &CheckVarResolver{Env: env, HasRuntime: false}, err
	}

	mergeRuntimeVars(env, meta, inspection, deployName, instance)
	return &CheckVarResolver{Env: env, HasRuntime: true}, nil
}

// buildTimeVars populates the BoxMetadata-derived subset of the variable
// map. This is the only part of the map available at `charly check box` time.
func buildTimeVars(meta *BoxMetadata, instance string) map[string]string {
	env := map[string]string{}
	if meta == nil {
		return env
	}
	if meta.User != "" {
		env["USER"] = meta.User
	}
	if meta.Home != "" {
		env["HOME"] = meta.Home
	}
	if meta.UID != 0 {
		env["UID"] = strconv.Itoa(meta.UID)
	}
	if meta.GID != 0 {
		env["GID"] = strconv.Itoa(meta.GID)
	}
	if meta.Box != "" {
		env["IMAGE"] = meta.Box
	}
	if meta.DNS != "" {
		env["DNS"] = meta.DNS
	}
	if meta.AcmeEmail != "" {
		env["ACME_EMAIL"] = meta.AcmeEmail
	}
	if instance != "" {
		env["INSTANCE"] = instance
	}
	return env
}

// mergeRuntimeVars augments env with values extracted from a container
// inspection: IP, hostname, HOST_PORT:<N>, VOLUME_PATH:<name>,
// VOLUME_CONTAINER_PATH:<name>, ENV_<NAME>.
//
// For volume mapping, the candy-declared short name is recovered by
// stripping the "charly-<image>-" prefix from the engine's full volume name.
// Bind mounts without a named volume are cross-referenced by destination
// path against meta.Volume to recover the short name.
func mergeRuntimeVars(env map[string]string, meta *BoxMetadata, c *ContainerInspection, deployName, instance string) {
	if c == nil {
		return
	}

	// Container name: strip leading "/" which Docker prefixes on inspect.
	name := strings.TrimPrefix(c.Name, "/")
	if name != "" {
		env["CONTAINER_NAME"] = name
	}

	// IP: prefer a per-network record if the top-level is empty (rootless podman).
	ip := c.NetworkSettings.IPAddress
	if ip == "" {
		for _, n := range c.NetworkSettings.Networks {
			if n.IPAddress != "" {
				ip = n.IPAddress
				break
			}
		}
	}
	if ip != "" {
		env["CONTAINER_IP"] = ip
	}

	// HOST_PORT:<container-port> → effective host port from the first binding.
	// Host-networked containers have an empty NetworkSettings.Ports but every
	// container port IS the host port — populate HOST_PORT:<N> = <N> from
	// the image metadata so test vars resolve correctly under network: host.
	if c.IsHostNetworked() {
		if meta != nil {
			for _, p := range meta.Port {
				containerPort := p
				if _, after, ok := strings.Cut(p, ":"); ok {
					containerPort = after
				}
				if j := strings.IndexByte(containerPort, '/'); j >= 0 {
					containerPort = containerPort[:j]
				}
				if containerPort != "" {
					env["HOST_PORT:"+containerPort] = containerPort
				}
			}
		}
	} else {
		for k, binds := range c.NetworkSettings.Ports {
			portStr := k
			if before, _, ok := strings.Cut(k, "/"); ok {
				portStr = before // strip "/tcp" / "/udp"
			}
			if len(binds) == 0 {
				continue
			}
			hostPort := binds[0].HostPort
			if hostPort == "" {
				continue
			}
			env["HOST_PORT:"+portStr] = hostPort
		}
	}

	// VOLUME_PATH / VOLUME_CONTAINER_PATH — short name comes from either the
	// volume's charly-<image>- (or charly-<image>-<instance>-) prefix or the image
	// metadata's Volumes list. BareVolumeName handles both prefix forms
	// uniformly — same helper that data.go and volumes.go use.
	destToShort := map[string]string{}
	if meta != nil && meta.Box != "" {
		for _, v := range meta.Volume {
			destToShort[v.ContainerPath] = BareVolumeName(v.VolumeName, meta.Box, instance)
		}
	}
	for _, m := range c.Mounts {
		short := ""
		if m.Name != "" && meta != nil && meta.Box != "" {
			// c.Mounts are the container's ACTUAL (deploy-scoped) volume names,
			// so strip by the deploy key, not the image. meta.Volume above is
			// image-label-named, so that loop keeps using meta.Box.
			short = BareVolumeName(m.Name, deployName, instance)
			if short == m.Name {
				short = "" // not one of ours — fall through to dest lookup
			}
		}
		if short == "" {
			short = destToShort[m.Destination]
		}
		if short == "" {
			continue
		}
		env["VOLUME_PATH:"+short] = m.Source
		env["VOLUME_CONTAINER_PATH:"+short] = m.Destination
	}

	// ENV_<NAME>: effective env vars from the container.
	for _, kv := range c.Config.Env {
		idx := strings.IndexByte(kv, '=')
		if idx <= 0 {
			continue
		}
		env["ENV_"+kv[:idx]] = kv[idx+1:]
	}
}
