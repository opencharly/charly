// feature.cue — wire types for the externalized `charly feature` command plugin
// (candy/plugin-feature; SDD conversion, per the standing operator directive: a
// hand-written wire struct not yet CUE-sourced is conversion-in-progress, never a
// sanctioned exception). NOT authoring kinds (never in #Node/#Op) — pure generated
// wire structs. The command LOGIC (the list/pending/validate grammar + output
// formatting, INCLUDING the plan-to-summary transform: keyword/text/agent/check
// flattening + validatePlanSteps) lives in the plugin (kit.KeywordOf /
// kit.ValidatePlanSteps / deploykit.DescriptionInfo are sdk-portable — the plugin
// calls them directly); the project ENUMERATION is PLUGIN-SIDE too (K-wave 2 cone
// R6 — the former "feature" HostBuild seam is DELETED): candy/plugin-feature loads
// the project over the reverse channel via loaderkit (LoadUnifiedViaExecutor +
// ProjectCandiesScanned + FinalizeScannedCandies) and flattens every entity's RAW
// description + plan (spec.Step, no transform), then computes
// summary/steps/validation itself. The seam's own #FeatureRequest / #FeatureReply
// wire envelope died with the seam — the plugin drives the load in-process, so no
// request/reply crosses the boundary; #FeatureEntity remains the in-memory
// enumeration shape. Plain structs — gengotypes generates them faithfully, no
// disjunction needed.

// #FeatureEntity is one enumerated kind: entity + its RAW plan data (Step is
// already a plain CUE-sourced wire type, so no separate flattened form is
// needed on the wire). An entity with neither a description nor a plan is
// still listed (as "(no description)") but the plugin skips
// summarizing/validating it, matching the former engine.
#FeatureEntity: {
	kind!: string @go(Kind)
	name!: string @go(Name)
	description?: string @go(Description)
	plan?: [...#Step] @go(Plan)
}
