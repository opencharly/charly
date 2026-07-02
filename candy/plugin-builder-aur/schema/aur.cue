// Self-contained input schema for the builder:aur capability — references no base def, so it
// compiles standalone (the SDK's serve-side compile). A builder authors no plugin_input (it is
// TRIGGERED by detection — a candy's aur: package section — never by an authored field), so this def
// carries no fields; it ships so the schema travels with the plugin (non-empty, base ++ plugin splice).
#AurBuilderInput: {
}
