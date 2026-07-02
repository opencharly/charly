// Self-contained input schema for the builder:examplebuilder capability — references
// no base def, so it compiles standalone (the SDK's serve-side compile). A builder
// authors no plugin_input, so this def is unused at authoring time; it ships so the
// schema travels with the plugin (non-empty, base ++ plugin splice) exactly like a
// verb/step plugin's input def.
#ExamplebuilderInput: {
	marker?: string
}
