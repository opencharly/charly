// schema/exampledispatch.cue — the SELF-CONTAINED CUE def for the F10 dispatch demo verb's input.
#ExampledispatchInput: {
	target_word?:     string // a verb word the host resolves + invokes on this plugin's behalf (plugin↔plugin)
	build_candy_dir?: string // a candy dir the host builds a plugin binary for (host-build)
	build_name?:      string
}
