// CLI reflection is a wire contract between command plugins, the Charly host,
// and MCP. It is CUE-owned like every other authored or transported shape.
#CLIArg: {
	prop: string & !=""
	name: string
	type: "string" | "boolean" | "integer" | "number" | "array" | "object"
	help?: string
	enum?: [...string]
	default?: string
	has_default?: bool @go(HasDefault)
	required?: bool
	is_bool?: bool @go(IsBool)
	is_slice?: bool @go(IsSlice)
	is_map?: bool @go(IsMap)
	negated?: bool
	elem_type?: "string" | "boolean" | "integer" | "number" | "array" | "object" @go(ElemType)
}

#CLILeaf: {
	path: string & !=""
	help?: string
	hidden?: bool
	positionals?: [...#CLIArg]
	flags?: [...#CLIArg]
}

#CLIModel: {
	name: string & !=""
	version: string
	leaves: [...#CLILeaf]
}
