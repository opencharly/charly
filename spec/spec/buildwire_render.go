package spec

// buildwire_render.go — the OpResolve render-context assembler (#55 coneK3tasks,
// relocated from sdk/deploykit/builders_render.go). Pure value assembly over spec types
// (*BuilderDef — an alias for *Builder, *BuildStageContext) + the spec cache-mount render
// primitives (cache_mount_render.go) — no *ResolvedBox, no *Generator, no
// deploykit/buildkit import. Shared by the box-build path (sdk/deploykit
// resolveDetectionBuilderReply), the pod-overlay build-emit (candy/plugin-installstep, via
// the deploykit re-export) + the INLINE-builder resolve (sdk/deploykit's dg.ResolveInlineBuilder;
// K-wave 2 cone R1 moved that leg plugin-side, deleting the host seam that used to call this),
// R3 — hence the deploykit re-export of this symbol. The coneSpecBuilder precedent
// (builder_resolve.go: builder value-primitives → spec/spec) EXTENDS, and the cone-render
// precedent (render_seam.go: render primitive → spec/spec so the host shares ONE source
// with no kit import) is the template.
//
// Relocating BuilderResolveInputFrom here lets charly/tasks.go shed its LAST sdk/deploykit
// import (the sole remaining prod importer across all sdk kits) by calling
// spec.BuilderResolveInputFrom directly — charly already imports spec. The cache-mount
// pre-render moves with it (cache_mount_render.go), so the assembler never reaches a kit.

// BuilderResolveInputFrom builds the serializable BuilderResolveInput a builder plugin's
// OpResolve leg needs, from the host-computed BuildStageContext. Cache mounts are
// PRE-RENDERED to flag strings here (RenderCacheMounts) with the SAME separator/trailing
// the former cacheMountsOwned/cacheMountsAuto template funcs used, so the plugin's rendered
// stage is byte-identical to the former embedded-vocabulary render. Shared by the box-build
// path (sdk/deploykit resolveDetectionBuilderReply) AND the pod-overlay build-emit
// (candy/plugin-installstep stepEmitBuilder, via the deploykit re-export) + the INLINE-builder
// resolve (sdk/deploykit's dg.ResolveInlineBuilder), R3 — hence exported.
func BuilderResolveInputFrom(candyName, builderName string, builderDef *BuilderDef, ctx *BuildStageContext) BuilderResolveInput {
	return BuilderResolveInput{
		Candy:            candyName,
		Builder:          builderName,
		BuilderRef:       ctx.BuilderRef,
		StageName:        ctx.StageName,
		LayerStage:       ctx.LayerStage,
		CopySrc:          ctx.CopySrc,
		UID:              ctx.UID,
		GID:              ctx.GID,
		Home:             ctx.Home,
		User:             ctx.User,
		Manifest:         ctx.Manifest,
		HasLockFile:      ctx.HasLockFile,
		InstallCmd:       ctx.InstallCmd,
		ManylinuxFix:     ctx.ManylinuxFix,
		HasBuildScript:   ctx.HasBuildScript,
		BuildScript:      ctx.BuildScript,
		Packages:         ctx.Packages,
		Options:          ctx.Options,
		CacheMountsOwned: RenderCacheMounts(ctx.CacheMounts, ctx.UID, ctx.GID, " \\\n    ", true),
		CacheMountsAuto:  RenderCacheMountsAuto(ctx.CacheMounts, ctx.UID, ctx.GID, " \\\n    ", false),
		Inline:           builderDef.Inline,
	}
}
