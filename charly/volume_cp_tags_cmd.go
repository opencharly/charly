package main

// volume_cp_tags_cmd.go — charly-CLI verbs: sidecar-aware
// exec/logs resolution, per-deployment volume probing + single-volume reset,
// host↔container file copy, and local image-tag listing. Each replaces a
// documented ad-hoc podman instruction (CLAUDE.md R4 / Key Rules).

import (
	"fmt"
	"os"
	"os/exec"
	"path"
	"sort"
	"strings"
)

// resolveSidecarContainer resolves the engine + container name of a deploy's
// SIDECAR container (charly-<box>[-<instance>]-<sidecar>) — the venue
// `charly cmd --sidecar` / `charly logs --sidecar` / `charly cp --sidecar`
// address, since the app-container resolver cannot reach it.
func resolveSidecarContainer(box, instance, sidecar string) (engine, name string, err error) {
	rt, err := ResolveRuntime()
	if err != nil {
		return "", "", err
	}
	boxName := resolveBoxName(box)
	runEngine := ResolveBoxEngineForDeploy(boxName, instance, rt.RunEngine)
	engine = EngineBinary(runEngine)
	name = SidecarContainerNameInstance(boxName, instance, sidecar)
	if !containerRunning(engine, name) {
		return "", "", fmt.Errorf("sidecar container %s is not running", name)
	}
	return engine, name, nil
}

// VolumeCmd groups the named-volume verbs.
type VolumeCmd struct {
	List  VolumeListCmd  `cmd:"" help:"List a deployment's charly-managed named volumes with their backing mountpoints"`
	Reset VolumeResetCmd `cmd:"" help:"Remove ONE named volume so the next start recreates it fresh (e.g. wipe a sidecar's state volume to force re-auth)"`
}

// VolumeListCmd lists the engine-side named volumes belonging to a
// deployment (app + sidecar volumes alike), with their host mountpoints —
// the charly-native replacement for ad-hoc `podman volume ls/inspect`.
type VolumeListCmd struct {
	Box      string `arg:"" help:"Box / deploy name"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *VolumeListCmd) Run() error {
	rt, err := ResolveRuntime()
	if err != nil {
		return err
	}
	boxName := resolveBoxName(c.Box)
	bin := EngineBinary(ResolveBoxEngineForDeploy(boxName, c.Instance, rt.RunEngine))
	prefix := containerNameInstance(boxName, c.Instance) + "-"
	out, err := exec.Command(bin, "volume", "ls", "--format", "{{.Name}}").Output()
	if err != nil {
		return fmt.Errorf("listing volumes: %w", err)
	}
	var names []string
	for n := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		if n != "" && strings.HasPrefix(n, prefix) {
			names = append(names, n)
		}
	}
	if len(names) == 0 {
		fmt.Printf("No named volumes for %s (prefix %s)\n", boxName, prefix)
		return nil
	}
	sort.Strings(names)
	for _, n := range names {
		mp, mpErr := exec.Command(bin, "volume", "inspect", "--format", "{{.Mountpoint}}", n).Output()
		mount := strings.TrimSpace(string(mp))
		if mpErr != nil {
			mount = "(mountpoint unavailable)"
		}
		fmt.Printf("%s\t%s\n", n, mount)
	}
	return nil
}

// VolumeResetCmd removes ONE named volume so the next `charly start`
// recreates it fresh — the charly-native replacement for the retired
// `podman volume rm <name>` re-initialization path (sidecar state wipes,
// corrupted caches). The engine refuses an in-use volume, so a running
// deployment surfaces an actionable error instead of silent data loss.
type VolumeResetCmd struct {
	Box      string `arg:"" help:"Box / deploy name"`
	Name     string `arg:"" help:"Volume name — bare (e.g. tailscale-state) or the full charly-<box>-<name> form"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *VolumeResetCmd) Run() error {
	rt, err := ResolveRuntime()
	if err != nil {
		return err
	}
	boxName := resolveBoxName(c.Box)
	bin := EngineBinary(ResolveBoxEngineForDeploy(boxName, c.Instance, rt.RunEngine))
	full := c.Name
	if !strings.HasPrefix(full, "charly-") {
		full = containerNameInstance(boxName, c.Instance) + "-" + c.Name
	}
	if out, err := exec.Command(bin, "volume", "rm", full).CombinedOutput(); err != nil {
		msg := strings.TrimSpace(string(out))
		if strings.Contains(msg, "no such volume") {
			return fmt.Errorf("volume %s does not exist", full)
		}
		return fmt.Errorf("removing volume %s: %s — an in-use volume is refused; stop the deployment first (`charly stop %s`)", full, msg, boxName)
	}
	fmt.Printf("Removed volume %s — the next `charly start %s` recreates it fresh\n", full, boxName)
	return nil
}

// CpCmd copies a file between the host and a running container (app or
// sidecar) — the charly-native replacement for ad-hoc `podman cp`. Exactly
// one of <src>/<dst> carries the ':' prefix marking the container side.
type CpCmd struct {
	Box      string `arg:"" help:"Box / deploy name"`
	Src      string `arg:"" help:"Source path — prefix with ':' for the container side"`
	Dst      string `arg:"" help:"Destination path — prefix with ':' for the container side"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
	Sidecar  string `long:"sidecar" help:"Target the named SIDECAR container instead of the app container"`
}

func (c *CpCmd) Run() error {
	srcInCtr := strings.HasPrefix(c.Src, ":")
	dstInCtr := strings.HasPrefix(c.Dst, ":")
	if srcInCtr == dstInCtr {
		return fmt.Errorf("exactly one of <src>/<dst> must carry the ':' container-side prefix (got src=%q dst=%q)", c.Src, c.Dst)
	}
	var engine, name string
	var err error
	if c.Sidecar != "" {
		engine, name, err = resolveSidecarContainer(c.Box, c.Instance, c.Sidecar)
	} else {
		engine, name, err = resolveContainer(c.Box, c.Instance)
	}
	if err != nil {
		return err
	}
	src, dst := c.Src, c.Dst
	if srcInCtr {
		src = name + ":" + strings.TrimPrefix(src, ":")
	} else {
		dst = name + ":" + strings.TrimPrefix(dst, ":")
	}
	cmd := exec.Command(engine, "cp", src, dst)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s cp %s %s: %w", engine, src, dst, err)
	}
	return nil
}

// ListTagsCmd lists the locally stored CalVer tags of charly-built images,
// newest first per box — tag discovery for rollbacks
// (`charly update <box> --tag <calver>`) and cache forensics, replacing
// ad-hoc `podman image ls`.
type ListTagsCmd struct {
	Box string `arg:"" optional:"" help:"Limit to one box short name"`
}

func (c *ListTagsCmd) Run() error {
	rt, err := ResolveRuntime()
	if err != nil {
		return err
	}
	groups, err := charlyImageTags(rt.RunEngine)
	if err != nil {
		return err
	}
	boxes := make([]string, 0, len(groups))
	for b := range groups {
		if c.Box != "" && b != c.Box {
			continue
		}
		boxes = append(boxes, b)
	}
	if len(boxes) == 0 {
		return fmt.Errorf("no locally stored charly images%s", map[bool]string{true: " for box " + c.Box, false: ""}[c.Box != ""])
	}
	sort.Strings(boxes)
	for _, b := range boxes {
		for _, t := range groups[b] {
			inUse := ""
			if t.InUse {
				inUse = "\t(in use)"
			}
			version := "-"
			if t.OkLabel {
				version = t.LabelCalVer.String()
			}
			fmt.Printf("%s\t%s\t%s%s\n", b, t.Ref, version, inUse)
		}
	}
	return nil
}

// matchImageGlob matches a glob against a full image ref OR its last path
// segment (repo:tag), so 'charly-fedora-2*' matches
// 'ghcr.io/opencharly/charly-fedora-2…:tag' without the registry prefix.
func matchImageGlob(glob, ref string) bool {
	last := ref
	if i := strings.LastIndex(last, "/"); i >= 0 {
		last = last[i+1:]
	}
	full, _ := path.Match(glob, ref)
	short, _ := path.Match(glob, last)
	return full || short
}

// invalidateImageTags removes every charly-labeled image tag matching the
// glob (full ref or its last path segment) — targeted cache invalidation
// for stale intermediates, replacing ad-hoc `podman rmi '<glob>'`. The
// retention safety rules apply unchanged: in-use images are skipped and
// `rmi` runs without -f as the backstop.
func invalidateImageTags(engine, glob string, dryRun bool) ([]string, error) {
	groups, err := charlyImageTags(engine)
	if err != nil {
		return nil, err
	}
	var removed []string
	for _, tags := range groups {
		for _, t := range tags {
			if !matchImageGlob(glob, t.Ref) {
				continue
			}
			if t.InUse {
				continue
			}
			if dryRun {
				removed = append(removed, t.Ref)
				continue
			}
			if err := exec.Command(EngineBinary(engine), "rmi", t.Ref).Run(); err != nil {
				continue // in-use backstop — engine refuses, same as retention
			}
			removed = append(removed, t.Ref)
		}
	}
	sort.Strings(removed)
	return removed, nil
}
