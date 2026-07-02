package kit

// builder_resolve.go — the SINGLE shared implementation of the four detection-builders'
// BUILD-TIME multi-stage render (pixi / npm / aur / cargo), R3. It is the build-time
// counterpart of builder.go's DEPLOY-time legs (BuilderCollectContext / BuilderReverse):
// where those carry the per-candy stage context + teardown ops out-of-process, THIS renders
// the multi-stage build itself out-of-process (via each builder plugin's OpResolve leg), so
// a builder's build-time multi-stage is resolved BY THE PLUGIN — no longer the in-core
// embedded builder: vocabulary (the former generate.go emitBuilderStages / emitBuilderArtifacts
// StageTemplate render).
//
// The four stage templates below are the VERBATIM former embedded builder: vocabulary
// stage_template / install_template strings (charly/charly.yml), relocated here as the ONE
// source both the OUT-OF-PROCESS box-build path (the plugin's OpResolve) and the IN-PROC
// pod-overlay build-emit (stepEmitBuilder) render. The ONLY change from the vocab text: the
// two cache-mount template FUNC calls ({{cacheMountsOwned …}} / {{cacheMountsAuto …}}) became
// pre-rendered input fields ({{.CacheMountsOwned}} / {{.CacheMountsAuto}}) — the HOST renders
// the cache-mount flag strings (it owns the cache_mount vocab + the RenderCacheMounts helper)
// and passes them in BuilderResolveInput, so kit needs no cache-mount render engine and the
// emitted bytes stay byte-identical to the former embedded-vocabulary render.
//
// Selection stays DETECTION host-side (candyNeedsBuilder against the retained detect_file /
// detect_config vocabulary); the host computes the full BuilderResolveInput (builder ref,
// stage name, target identity, filesystem-detected manifest/lockfile/build-script, aur
// packages/options, pre-rendered cache mounts) and Invokes the plugin's OpResolve; the plugin
// returns the rendered Stage + CopyArtifacts (+ CopyBinary / InlineFragment) via THIS function.

import (
	"fmt"
	"strings"
	"text/template"

	"github.com/opencharly/charly/charly/spec"
)

// pixiStageTemplate is the verbatim former builder.pixi.stage_template (cache-mount func →
// pre-rendered {{.CacheMountsOwned}}).
const pixiStageTemplate = "FROM {{.BuilderRef}} AS {{.StageName}}\nUSER {{.UID}}\nWORKDIR {{.Home}}\n{{- if .HasLockFile}}\nCOPY --chown={{.UID}}:{{.GID}} {{.CopySrc}}/pixi.lock pixi.lock\n{{- end}}\nCOPY --chown={{.UID}}:{{.GID}} {{.CopySrc}}/{{.Manifest}} {{.Manifest}}\n{{.ManylinuxFix}}\nENV PIXI_CACHE_DIR=/tmp/pixi-cache RATTLER_CACHE_DIR=/tmp/rattler-cache\n{{- if .HasBuildScript}}\n# build.sh is COPY'd (NOT bind-mounted) so its CONTENT is part of this\n# stage's BuildKit cache key — editing build.sh (e.g. the pixelflux\n# NVENC patch) MUST invalidate the compile. A\n# `--mount=type=bind,from=<stage>,source=/build.sh` delivers the file\n# but its content NEVER enters the RUN cache key (the key is parent-SHA\n# + COPY'd manifest + RUN-text), so a changed build.sh silently reused\n# a stale compiled artifact — the \"new code not picked up\" bug. COPY\n# keys it exactly like pixi.toml / pixi.lock above.\nCOPY --chown={{.UID}}:{{.GID}} {{.CopySrc}}/{{.BuildScript}} /tmp/{{.BuildScript}}\nRUN {{.CacheMountsOwned}}{{.InstallCmd}} && bash /tmp/{{.BuildScript}} && rm -f {{.Manifest}} pixi.lock\n{{- else}}\nRUN {{.CacheMountsOwned}}{{.InstallCmd}} && rm -f {{.Manifest}} pixi.lock\n{{- end}}\n"

// npmStageTemplate is the verbatim former builder.npm.stage_template.
const npmStageTemplate = "FROM {{.BuilderRef}} AS {{.StageName}}\nUSER {{.UID}}\nWORKDIR {{.Home}}\n# Override NPM_CONFIG_PREFIX from the builder image so npm writes to\n# the TARGET image's HOME (not the builder's /home/user). Without this,\n# uid=0 target images silently get empty copy_artifacts.\nENV NPM_CONFIG_PREFIX={{.Home}}/.npm-global\nCOPY --chown={{.UID}}:{{.GID}} {{.CopySrc}}/package.json package.json\nRUN {{.CacheMountsOwned}}node -e 'var d=require(\"./package.json\").dependencies||{};for(var[n,v]of Object.entries(d))console.log(v===\"*\"?n:n+\"@\"+v)' | xargs npm install -g && rm -f package.json\n"

// aurStageTemplate is the verbatim former builder.aur.stage_template (cache-mount func →
// pre-rendered {{.CacheMountsAuto}}).
const aurStageTemplate = "FROM {{.BuilderRef}} AS {{.StageName}}\nUSER root\nRUN echo '{{.User}} ALL=(ALL) NOPASSWD: ALL' > /etc/sudoers.d/builder\nUSER {{.UID}}\nWORKDIR {{.Home}}\nENV XDG_CACHE_HOME=/tmp/aur-xdg-cache\nRUN {{.CacheMountsAuto}} \\\n    mkdir -p /tmp/aur-build /tmp/aur-srcdest /tmp/aur-xdg-cache && \\\n    cp /etc/makepkg.conf /tmp/makepkg.conf && \\\n    sed -i '/^OPTIONS/s/ debug/ !debug/' /tmp/makepkg.conf && \\\n    echo 'SRCDEST=/tmp/aur-srcdest' >> /tmp/makepkg.conf && \\\n    sudo pacman -Syu --noconfirm && \\\n    yay -S --noconfirm --needed --builddir /tmp/aur-build --makepkgconf /tmp/makepkg.conf\n{{- range .Options}} {{.}}{{end}}\n{{- range .Packages}} \\\n      {{.}}{{end}} && \\\n    mkdir -p /tmp/aur-pkgs && \\\n    find /tmp/aur-build -name '*.pkg.tar.zst' -exec cp {} /tmp/aur-pkgs/ \\;\n"

// cargoInlineTemplate is the verbatim former builder.cargo.install_template (cache-mount func
// → pre-rendered {{.CacheMountsOwned}}). cargo is an INLINE builder — no separate FROM stage;
// this RUN emits IN the main image, returned as BuilderResolveReply.InlineFragment.
const cargoInlineTemplate = "RUN --mount=type=bind,from={{.LayerStage}},source=/,target=/ctx \\\n    {{.CacheMountsOwned}}cargo install --path /ctx\n"

// BuilderResolve renders `word`'s build-time multi-stage from the host-supplied context,
// returning the pieces the host splices into the Containerfile: Stage (pre-main-FROM),
// CopyArtifacts + CopyBinary (post-main-FROM), or InlineFragment (in-candy, inline builders).
// An unknown word is a LOUD error (never a silent empty stage). This is the ONE render both
// the box-build plugin OpResolve and the in-proc pod-overlay build-emit call (R3).
func BuilderResolve(word string, in spec.BuilderResolveInput) (spec.BuilderResolveReply, error) {
	var zero spec.BuilderResolveReply
	switch word {
	case "pixi":
		stage, err := renderBuilderStage("pixi-stage", pixiStageTemplate, in)
		if err != nil {
			return zero, err
		}
		return spec.BuilderResolveReply{
			Stage:         stage,
			CopyArtifacts: []string{builderCopyLine(in.StageName, in.Home, in.Home, true, in.UID, in.GID)},
			CopyBinary:    builderCopyLine(in.StageName, "/usr/local/bin/pixi", "/usr/local/bin/pixi", false, 0, 0),
		}, nil
	case "npm":
		stage, err := renderBuilderStage("npm-stage", npmStageTemplate, in)
		if err != nil {
			return zero, err
		}
		return spec.BuilderResolveReply{
			Stage:         stage,
			CopyArtifacts: []string{builderCopyLine(in.StageName, in.Home, in.Home, true, in.UID, in.GID)},
		}, nil
	case "aur":
		stage, err := renderBuilderStage("aur-stage", aurStageTemplate, in)
		if err != nil {
			return zero, err
		}
		return spec.BuilderResolveReply{
			Stage:         stage,
			CopyArtifacts: []string{builderCopyLine(in.StageName, "/tmp/aur-pkgs/", "/tmp/aur-pkgs/", false, 0, 0)},
		}, nil
	case "cargo":
		frag, err := renderBuilderStage("cargo-inline", cargoInlineTemplate, in)
		if err != nil {
			return zero, err
		}
		return spec.BuilderResolveReply{InlineFragment: frag}, nil
	}
	return zero, fmt.Errorf("kit.BuilderResolve: unknown detection-builder word %q", word)
}

// renderBuilderStage executes a relocated builder stage template against the resolve input.
// The templates use only stdlib text/template constructs (field access, if/range) — the
// cache-mount funcs were pre-rendered host-side — so no FuncMap is needed.
func renderBuilderStage(name, tmplStr string, in spec.BuilderResolveInput) (string, error) {
	t, err := template.New(name).Parse(tmplStr)
	if err != nil {
		return "", fmt.Errorf("parse %s template: %w", name, err)
	}
	var b strings.Builder
	if err := t.Execute(&b, in); err != nil {
		return "", fmt.Errorf("render %s template: %w", name, err)
	}
	return b.String(), nil
}

// builderCopyLine renders a `COPY --from=<stage> [--chown=uid:gid] <src> <dst>` directive —
// the exact shape the former emitBuilderArtifacts produced (copy_artifact / copy_binary),
// with no trailing newline (the host adds newlines when splicing).
func builderCopyLine(stage, src, dst string, chown bool, uid, gid int) string {
	if chown {
		return fmt.Sprintf("COPY --from=%s --chown=%d:%d %s %s", stage, uid, gid, src, dst)
	}
	return fmt.Sprintf("COPY --from=%s %s %s", stage, src, dst)
}
