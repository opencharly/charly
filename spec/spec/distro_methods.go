package spec

import "sort"

// distro_methods.go — pure Go METHODS on the CUE-generated ResolvedDistro
// (sdk/schema/distro.cue -> spec/cue_types_gen.go). `cue exp gengotypes` has no
// construct for a method — it generates ONLY the field shape — so these stay
// hand-written here, mirroring Op.Kind() in spec/charly_methods.go: a method,
// not a type. Ported verbatim from the former hand-written ResolvedDistro
// (sdk/spec/distro_wire.go, deleted by the SDD conversion).

// PrimaryFormat returns the distro's primary (non-secondary) build format —
// the deterministic first non-secondary Format name (mirrors
// spec.Distro.PrimaryFormat).
func (d *ResolvedDistro) PrimaryFormat() string {
	if d == nil {
		return ""
	}
	names := make([]string, 0, len(d.Format))
	for name := range d.Format {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if fd := d.Format[name]; fd != nil && fd.Secondary {
			continue
		}
		return name
	}
	return ""
}

// LocalPkgFormat picks the format whose local_pkg block drives the charly
// package INSTALL (the download leg): the caller's primary format, then
// PrimaryFormat, then any localpkg-capable format (mirrors
// spec.Distro.LocalPkgFormat).
func (d *ResolvedDistro) LocalPkgFormat(primaryFormat string) (string, *LocalPkg) {
	if d == nil {
		return "", nil
	}
	for _, fmtName := range []string{primaryFormat, d.PrimaryFormat()} {
		if fmtName == "" {
			continue
		}
		if fd := d.Format[fmtName]; fd != nil && fd.LocalPkg != nil {
			return fmtName, fd.LocalPkg
		}
	}
	names := make([]string, 0, len(d.Format))
	for name := range d.Format {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if fd := d.Format[name]; fd != nil && fd.LocalPkg != nil {
			return name, fd.LocalPkg
		}
	}
	return "", nil
}
