package sdk

// climodel.go is the host-CLI-description contract emitted by `charly __cli-model`
// (a hidden core command that reflects charly's kong model) and consumed by an
// external COMMAND-class plugin that needs to mirror the WHOLE charly CLI without
// importing the package-main `CLI` tree — the `charly mcp serve` MCP bridge is the
// motivating consumer (candy/plugin-mcp). The host serializes its kong leaf set into
// this shape; the plugin rebuilds its tool surface from it and drives each command by
// fork/exec'ing `charly <path…> <args>`. Shared here (R3) so the emit + decode sides
// cannot drift.
//
// This travels over fork/exec STDOUT as JSON (not the gRPC provider channel), so it is
// a plain JSON contract, not a proto message.

// CLIModel is the full command-tree description of a `charly` binary.
type CLIModel struct {
	Name    string    `json:"name"`    // "charly"
	Version string    `json:"version"` // CharlyVersion()
	Leaves  []CLILeaf `json:"leaves"`  // one per runnable leaf command
}

// CLILeaf is one runnable leaf command (e.g. "box.build"), with its arg surface.
type CLILeaf struct {
	Path        string   `json:"path"`                  // dotted, e.g. "box.build"
	Help        string   `json:"help,omitempty"`        // leaf help text
	Hidden      bool     `json:"hidden,omitempty"`      // hidden leaf (e.g. __plugin / __cli-model)
	Positionals []CLIArg `json:"positionals,omitempty"` // declared order — appended to argv as values
	Flags       []CLIArg `json:"flags,omitempty"`       // flattened across ancestor branches
}

// CLIArg describes one positional or flag. `Prop` is the snake_case JSON-schema
// property name an MCP tool exposes; `Name` is the original kong flag name used to
// build `--Name=value` argv (unused for positionals, which are positional in argv).
type CLIArg struct {
	Prop       string   `json:"prop"`                  // snake_case schema property name
	Name       string   `json:"name"`                  // original kong flag name (for --Name=…)
	Type       string   `json:"type"`                  // json-schema primitive: string/boolean/integer/number/array/object
	Help       string   `json:"help,omitempty"`        // arg help → schema description
	Enum       []string `json:"enum,omitempty"`        // allowed values
	Default    string   `json:"default,omitempty"`     // raw default string (coerced to Type by the consumer)
	HasDefault bool     `json:"has_default,omitempty"` // a default was declared
	Required   bool     `json:"required,omitempty"`    // required arg
	IsBool     bool     `json:"is_bool,omitempty"`     // bool flag (emits --Name / --no-Name, no value)
	IsSlice    bool     `json:"is_slice,omitempty"`    // accumulating slice (repeated --Name=… / multiple positionals)
	IsMap      bool     `json:"is_map,omitempty"`      // map flag (object schema)
	Negated    bool     `json:"negated,omitempty"`     // bool flag supports the --no- prefix
	ElemType   string   `json:"elem_type,omitempty"`   // slice element json-schema type
}
