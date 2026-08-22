package spec

// substrate_template_methods.go — pure Go METHODS on the CUE-generated
// ResolvedAndroid (sdk/schema/substrate_template.cue -> spec/cue_types_gen.go).
// `cue exp gengotypes` has no construct for a method — it generates ONLY the
// field shape — so these stay hand-written here, mirroring Op.Kind() in
// spec/charly_methods.go: a method, not a type. Ported verbatim from the
// former hand-written ResolvedAndroid (sdk/spec/substrate_template_wire.go,
// deleted by the SDD conversion).

// IsEndpoint reports whether the resolved device targets a remote adb
// endpoint.
func (a *ResolvedAndroid) IsEndpoint() bool {
	return a != nil && a.Adb != nil && a.Adb.Host != ""
}

// EffectiveSerial returns the device serial, defaulting to the emulator
// serial.
func (a *ResolvedAndroid) EffectiveSerial() string {
	if a != nil && a.Serial != "" {
		return a.Serial
	}
	return "emulator-5554"
}
