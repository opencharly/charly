package main

// volume_cp_tags_cmd.go — local image-tag listing. VolumeCmd/CpCmd (the DEPLOY wave) moved
// wholesale — with zero seam — to candy/plugin-pod: they needed no core-only type, only
// kit.ResolveBoxName/deploykit.ResolveBoxEngineForDeploy/deploykit.ResolveContainer/
// deploykit.ResolveSidecarContainer, all already SDK-portable. The former resolveSidecarContainer
// (this file's own bare duplicate of deploykit.ResolveSidecarContainer) dissolved into that
// deploykit twin (CHECK-wave container-resolve dedup) — its 2 callers (cmd.go,
// pod_lifecycle_resolve.go) now call deploykit.ResolveSidecarContainer directly.
//
// The tag INVENTORY (charlyImageTags) + invalidateImageTags/matchImageGlob relocated to
// candy/plugin-clean's retention engine (K1-alpha core-minimization) — ListTagsCmd now reaches
// it via verb:retention (retention_plugin.go's listCharlyImageTags), the SAME peer/core-adapter
// pattern verb:credential/verb:gpu/verb:tunnel already use.

import (
	"fmt"
	"os"
	"sort"
)

// ListTagsCmd lists the locally stored CalVer tags of charly-built images,
// newest first per box — tag discovery for rollbacks
// (`charly update <box> --tag <calver>`) and cache forensics, replacing
// ad-hoc `podman image ls`.
type ListTagsCmd struct {
	Box string `arg:"" optional:"" help:"Limit to one box short name"`
}

func (c *ListTagsCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	tags, err := listCharlyImageTags(dir)
	if err != nil {
		return err
	}
	byBox := map[string][]int{}
	for i, t := range tags {
		if c.Box != "" && t.Box != c.Box {
			continue
		}
		byBox[t.Box] = append(byBox[t.Box], i)
	}
	if len(byBox) == 0 {
		return fmt.Errorf("no locally stored charly images%s", map[bool]string{true: " for box " + c.Box, false: ""}[c.Box != ""])
	}
	boxes := make([]string, 0, len(byBox))
	for b := range byBox {
		boxes = append(boxes, b)
	}
	sort.Strings(boxes)
	for _, b := range boxes {
		for _, i := range byBox[b] {
			t := tags[i]
			inUse := ""
			if t.InUse {
				inUse = "\t(in use)"
			}
			fmt.Printf("%s\t%s\t%s%s\n", t.Box, t.Ref, t.Version, inUse)
		}
	}
	return nil
}
