package main

// volume_cp_tags_cmd.go — local image-tag listing. VolumeCmd/CpCmd (the DEPLOY wave) moved
// wholesale — with zero seam — to candy/plugin-pod: they needed no core-only type, only
// kit.ResolveBoxName/deploykit.ResolveBoxEngineForDeploy/deploykit.ResolveContainer/
// deploykit.ResolveSidecarContainer, all already SDK-portable. The former resolveSidecarContainer
// (this file's own bare duplicate of deploykit.ResolveSidecarContainer) dissolved into that
// deploykit twin (CHECK-wave container-resolve dedup) — its 2 callers (cmd.go,
// pod_lifecycle_resolve.go) now call deploykit.ResolveSidecarContainer directly.

import (
	"fmt"
	"os/exec"
	"path"
	"sort"
	"strings"

	"github.com/opencharly/sdk/kit"
)

// ListTagsCmd lists the locally stored CalVer tags of charly-built images,
// newest first per box — tag discovery for rollbacks
// (`charly update <box> --tag <calver>`) and cache forensics, replacing
// ad-hoc `podman image ls`.
type ListTagsCmd struct {
	Box string `arg:"" optional:"" help:"Limit to one box short name"`
}

func (c *ListTagsCmd) Run() error {
	rt, err := kit.ResolveRuntime()
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
			if err := exec.Command(kit.EngineBinary(engine), "rmi", t.Ref).Run(); err != nil {
				continue // in-use backstop — engine refuses, same as retention
			}
			removed = append(removed, t.Ref)
		}
	}
	sort.Strings(removed)
	return removed, nil
}
