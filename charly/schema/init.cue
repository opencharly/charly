// CUE schema for the `init` kind. #Init validates ONE value of the `init:` map
// (InitDef — supervisord/systemd). CLOSED: every authored key is modeled (an
// unknown key is a typo). *_template fields are Go text/template (plain
// `string`). No #Step (init has no plan).

#Init: {
	candy_field?: [...(string & !="")] @go(CandyFields)
	candy_file?: [...(string & !="")] @go(CandyFiles)
	depends_candy?: string & !="" @go(DependsCandy)
	requires_capability?: [...(string & =~"^[a-z][a-z0-9_]*$")] @go(RequiresCapability)

	// The one mandatory field: the build model.
	model: "fragment_assembly" | "file_copy"

	header_file?:    string & !="" @go(HeaderFile)
	fragment_dir?:   string & !="" @go(FragmentDir)
	relay_template?: string        @go(RelayTemplate)

	stage_name?:          string & !="" @go(StageName)
	stage_header_copy?:   string        @go(StageHeaderCopy)
	stage_fragment_copy?: string        @go(StageFragmentCopy)

	assembly_template?:      string @go(AssemblyTemplate)
	system_enable_template?: string @go(SystemEnableTemplate)
	post_assembly_template?: string @go(PostAssemblyTemplate)

	entrypoint?: [...(string & !="")]
	fallback_entrypoint?: [...(string & !="")] @go(FallbackEntrypoint)

	management_tool?: string & !="" @go(ManagementTool)
	management_command?: {
		[string]: string & !=""
	} @go(ManagementCommands)

	label_key?: string & =~"^ai\\.opencharly\\.service\\.[a-z0-9]+$" @go(LabelKey)

	service_schema?: #InitServiceSchema @go(ServiceSchema,optional=nillable)
}

#InitServiceSchema: {
	service_template?:     string @go(ServiceTemplate)
	unit_path_template?:   string @go(UnitPathTemplate)
	dropin_template?:      string @go(DropinTemplate)
	dropin_path_template?: string @go(DropinPathTemplate)
	supports_packaged?:    bool   @go(SupportsPackaged)
}
