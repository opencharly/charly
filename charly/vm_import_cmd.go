package main

import (
	"fmt"
	"os"
	"text/tabwriter"
)

// vm_import_cmd.go — Kong subcommand wiring for `charly vm import`.

// VmImportCmd implements `charly vm import {<domain>, --all, --list}`
// plus the reverse-sync surface (`--update`, `--diff`, `--show-drift`).
type VmImportCmd struct {
	// Domain is the libvirt domain name to import. Empty when --all
	// or --list is set.
	Domain string `arg:"" optional:"" help:"Libvirt domain name to adopt (e.g. as listed by 'virsh -c qemu:///session list --all'). Omit when using --all or --list."`

	// TargetName overrides the vm.yml entry key. Default: domain name
	// with any "charly-" prefix stripped.
	TargetName string `long:"target-name" help:"Override the kind:vm entry name in vm.yml"`

	// All adopts every libvirt domain not already in vm.yml.
	All bool `long:"all" help:"Adopt every unmanaged libvirt domain"`

	// List shows libvirt domains absent from vm.yml without writing
	// anything. Diagnostic mode.
	List bool `long:"list" help:"Show libvirt domains absent from vm.yml; do not write"`

	// ShowDrift, used with --list, marks entries whose libvirt XML
	// has diverged from the on-disk vm.yml entry.
	ShowDrift bool `long:"show-drift" help:"With --list: mark entries whose libvirt XML diverges from vm.yml"`

	// Update re-reads libvirt XML for an existing entry and overwrites
	// only source-derived fields, preserving operator-authored
	// sub-mappings (snapshots, cloud_init, ssh, libvirt). Compatible
	// with --diff (preview without writing).
	Update bool `long:"update" help:"Re-read libvirt XML and update an existing kind:vm entry in place"`

	// Diff prints the field-level differences between libvirt XML and
	// the on-disk vm.yml entry, without writing.
	Diff bool `long:"diff" help:"Print drift between libvirt XML and vm.yml without writing"`

	// ReplaceLibvirt, used with --update, drops the operator-authored
	// libvirt: block (defaults preserve it).
	ReplaceLibvirt bool `long:"replace-libvirt" help:"With --update: also overwrite the operator-authored libvirt: block"`
}

// Run executes `charly vm import`.
func (c *VmImportCmd) Run() error {
	if c.Diff {
		if c.Domain == "" {
			return fmt.Errorf("charly vm import --diff: domain name is required")
		}
		name := c.TargetName
		if name == "" {
			name = stripCharlyPrefix(c.Domain)
		}
		diffs, err := DiffImported(name, c.Domain)
		if err != nil {
			return err
		}
		if len(diffs) == 0 {
			fmt.Printf("%s: no drift\n", name)
			return nil
		}
		fmt.Printf("%s: %d drift(s):\n", name, len(diffs))
		for _, d := range diffs {
			fmt.Printf("  %s\n", d)
		}
		return nil
	}

	if c.Update {
		if c.Domain == "" {
			return fmt.Errorf("charly vm import --update: domain name is required")
		}
		name := c.TargetName
		if name == "" {
			name = stripCharlyPrefix(c.Domain)
		}
		spec, err := UpdateImportedVm(name, c.Domain, c.ReplaceLibvirt)
		if err != nil {
			return err
		}
		fmt.Printf("updated %s (last_synced_at=%s)\n", name, spec.Source.LastSyncedAt)
		return nil
	}

	if c.List {
		domains, err := ListUnmanagedDomains()
		if err != nil {
			return err
		}

		// Drift surfacing for ALREADY-imported entries (independent of
		// the unmanaged-domain table).
		var driftRows [][2]string
		if c.ShowDrift {
			rows, derr := scanDriftAcrossImports()
			if derr == nil {
				driftRows = rows
			} else {
				fmt.Fprintf(os.Stderr, "(drift scan: %v)\n", derr)
			}
		}

		if len(domains) == 0 && len(driftRows) == 0 {
			fmt.Println("no unmanaged libvirt domains")
			if c.ShowDrift {
				fmt.Println("no drift across imported entries")
			}
			return nil
		}
		tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		if len(domains) > 0 {
			fmt.Fprintln(tw, "DOMAIN\tWOULD-IMPORT-AS")
			for _, d := range domains {
				fmt.Fprintf(tw, "%s\t%s\n", d, stripCharlyPrefix(d))
			}
			fmt.Fprintln(tw, "")
		}
		if len(driftRows) > 0 {
			fmt.Fprintln(tw, "ENTRY\tDRIFT-SUMMARY")
			for _, r := range driftRows {
				fmt.Fprintf(tw, "%s\t%s\n", r[0], r[1])
			}
		}
		return tw.Flush()
	}

	if c.All {
		domains, err := ListUnmanagedDomains()
		if err != nil {
			return err
		}
		if len(domains) == 0 {
			fmt.Println("no unmanaged libvirt domains to import")
			return nil
		}
		var failed []string
		for _, d := range domains {
			if err := importOne(d, ""); err != nil {
				fmt.Fprintf(os.Stderr, "import %s: %v\n", d, err)
				failed = append(failed, d)
				continue
			}
			fmt.Printf("imported %s\n", d)
		}
		if len(failed) > 0 {
			return fmt.Errorf("import-all completed with %d failures: %v", len(failed), failed)
		}
		return nil
	}

	if c.Domain == "" {
		return fmt.Errorf("charly vm import: domain name is required (or pass --list / --all)")
	}
	if err := importOne(c.Domain, c.TargetName); err != nil {
		return err
	}
	fmt.Printf("imported %s\n", c.Domain)
	return nil
}

// importOne handles a single domain import end-to-end.
func importOne(domainName, targetName string) error {
	name, spec, err := ImportFromLibvirt(domainName, targetName)
	if err != nil {
		return err
	}
	if err := WriteVmImportDeclaration(name, spec); err != nil {
		return err
	}
	return nil
}

// stripCharlyPrefix removes a leading "charly-" if present (mirrors the
// default-target-name logic in ImportFromLibvirt).
func stripCharlyPrefix(s string) string {
	const p = "charly-"
	if len(s) > len(p) && s[:len(p)] == p {
		return s[len(p):]
	}
	return s
}

// scanDriftAcrossImports scans every kind:vm entry in vm.yml /
// charly.yml whose source.kind is "imported", and runs DiffImported
// against each. Returns rows of (entry-name, drift-summary) for the
// `--list --show-drift` table. Entries with no drift are omitted.
func scanDriftAcrossImports() ([][2]string, error) {
	uf, ok, err := loadUnifiedForImport()
	if err != nil {
		return nil, err
	}
	if !ok || uf == nil {
		return nil, nil
	}
	var rows [][2]string
	for name, spec := range uf {
		if spec == nil || spec.Source.Kind != "imported" {
			continue
		}
		domain := spec.Source.LibvirtName
		if domain == "" {
			domain = name
		}
		diffs, derr := DiffImported(name, domain)
		if derr != nil {
			rows = append(rows, [2]string{name, fmt.Sprintf("(scan failed: %v)", derr)})
			continue
		}
		if len(diffs) == 0 {
			continue
		}
		summary := fmt.Sprintf("drift: %d field(s)", len(diffs))
		rows = append(rows, [2]string{name, summary})
	}
	return rows, nil
}

// loadUnifiedForImport reads the project's vm: map. Centralized so the
// drift scanner doesn't duplicate file resolution.
func loadUnifiedForImport() (map[string]*VmSpec, bool, error) {
	cwd, err := osGetwd()
	if err != nil {
		return nil, false, err
	}
	uf, ok, lerr := LoadUnified(cwd)
	if lerr != nil {
		return nil, false, lerr
	}
	if !ok || uf == nil {
		return nil, false, nil
	}
	return uf.VM, true, nil
}

// osGetwd is a small indirection to make testing easier.
var osGetwd = os.Getwd
