// This plugin's OWN CUE schema — the typed plugin_input for the `wl`
// live-container check verb, served over the Describe channel. It is the SINGLE
// SOURCE for this plugin's params, used two ways (the same contract core `spec`
// and the reference plugin-http use):
//
//  1. GENERATE the Go param struct — `cue exp gengotypes` (driven by task cue:gen,
//     which wraps this with `package params` + `@go(params)`) emits
//     ../params/cue_types_gen.go, so the provider decodes plugin_input into a TYPED
//     struct, never a hand-parsed map.
//  2. VALIDATE authored input AT RUNTIME — the host splices this source onto the
//     base (base ++ plugin) and validates every authored `wl` step's plugin_input
//     against #WlInput.
//
// Every wl-EXCLUSIVE field lives here (they left core #Op in the schema-compaction
// cutover): the verb `method` enum (the former core #WlMethod, 38 methods incl. the
// overlay-*/sway-* nested ones) plus the input/window/artifact modifiers
// (x/y/x2/y2/button/direction/amount/target/text/key/combo/action/query/command +
// artifact/artifact_min_bytes/artifact_min_dimensions/artifact_not_uniform).
// `command` — the argv for `exec` / `sway-msg` — is ABSORBED from the former shared
// #Op command modifier (the residual #Op `command` field is the command plugin's
// internal rehydration target, never wl's). The step's shared modifiers stay on
// #Op, read off the marshalled step Op: the exit_status/stdout/stderr matchers
// (sdk.VerbVerdict).
//
// SELF-CONTAINED: it references NO base def, so it compiles standalone (gengotypes +
// the SDK's serve-side check) AND splices onto the base (base ++ plugin is a
// def-name collision check, not a base-reference resolver).
#WlInput: {
	// method — the wl verb method (the former core #WlMethod enum).
	method: "status" | "toplevel" | "windows" | "geometry" | "xprop" | "atspi" | "screenshot" | "clipboard" | "click" | "double-click" | "mouse" | "scroll" | "drag" | "type" | "key" | "key-combo" | "focus" | "close" | "fullscreen" | "minimize" | "exec" | "resolution" | "overlay-list" | "overlay-status" | "overlay-show" | "overlay-hide" | "sway-tree" | "sway-workspaces" | "sway-outputs" | "sway-msg" | "sway-focus" | "sway-move" | "sway-resize" | "sway-layout" | "sway-workspace" | "sway-kill" | "sway-floating" | "sway-reload"
	// x/y — desktop-absolute pointer coordinates (click/double-click/mouse/scroll/drag).
	x?: int @go(,type=int)
	y?: int @go(,type=int)
	// x2/y2 — the drag end coordinates.
	x2?: int @go(,type=int)
	y2?: int @go(,type=int)
	// button — pointer button (left/right/middle; empty defaults to left).
	button?: string
	// text — text to type / the clipboard payload / the overlay text / the exec-adjacent text.
	text?: string
	// key — a named XKB key for `key` (wtype -k).
	key?: string @go(KeyName)
	// combo — a key combination for `key-combo` (e.g. ctrl+shift+t).
	combo?: string
	// direction — scroll direction (up/down/left/right).
	direction?: string
	// amount — scroll step count (0 defaults to 3).
	amount?: int @go(,type=int)
	// target — the window/output/workspace target (focus/close/fullscreen/minimize/
	// geometry/xprop/overlay name/resolution WxH/sway-* argument).
	target?: string
	// action — the sub-action selector (atspi tree/find/click; clipboard get/set/clear).
	action?: string
	// query — the atspi find/click element query (name / role / "name:role").
	query?: string
	// command — the argv for `exec` / `sway-msg` (absorbed from the former shared
	// #Op command modifier).
	command?: string
	// artifact — the host path a `screenshot` writes its PNG to.
	artifact?: string
	// artifact_min_bytes — assert the artifact is at least N bytes.
	artifact_min_bytes?: int & >=0 @go(ArtifactMinBytes,type=int)
	// artifact_min_dimensions — assert the decoded image is at least WxH.
	artifact_min_dimensions?: string & =~"^[0-9]+x[0-9]+$" @go(ArtifactMinDimensions)
	// artifact_not_uniform — assert the image is not uniformly one color.
	artifact_not_uniform?: bool @go(ArtifactNotUniform)
}
