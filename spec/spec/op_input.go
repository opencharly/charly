package spec

// op_input.go — InputStr, the pure desugared-plugin-input string reader (#55 CHECK-ENGINE
// cone: relocated from sdk/kit/methodspec.go so charly core's verb dispatch reads a step's
// per-verb input while importing only spec). Since the schema-compaction cutover the per-verb
// fields live in Op.PluginInput (the `<word>: <input>` sugar desugars to Plugin/PluginInput;
// core #Op carries no per-verb fields), so this is the ONE reader every consumer shares. Pure
// (no host deps); sdk/kit re-exports it so the kit.InputStr / kit.Pos* call sites are untouched.

// InputStr reads a string field from the step's desugared plugin input map.
func InputStr(c *Op, key string) string {
	if c.PluginInput == nil {
		return ""
	}
	s, _ := c.PluginInput[key].(string)
	return s
}
