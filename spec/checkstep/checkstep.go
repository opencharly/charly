// Package checkstep hosts the TYPED-STEP state-provision contract cluster for host-coupled
// check-verb candies (#55 CHECK-ENGINE cone Option A): the StepKindName candy-contract string
// type + its consts, the StepDescriptor / ServicePackagedDesc / SystemPackagesDesc construction
// inputs, the ResolvePackageName cross-distro resolver, and the OPTIONAL StepProvider /
// ProvisionActor roles a kit candy implements alongside CheckVerbProvider.
//
// This cluster is its OWN package (not spec/spec) because the const IDENTIFIERS
// StepKindServicePackaged / StepKindSystemPackages clash in spec/spec with the INTERNAL
// InstallPlan IR enum (spec/spec/ir_enums.go's `StepKind` string enum, values "ServicePackaged" /
// "SystemPackages") — a deliberately different type (the candy-facing kit.StepKindName string,
// values "service-packaged" / "system-packages") that charly's kitStepKindToCharly MAPS onto
// the internal StepKind. Housing the candy contract here lets charly core's in-proc
// kitVerbAdapter (check_kit_adapter.go) reference it importing zero kit, while sdk/kit re-exports
// each symbol (sdk/kit/check_step_descriptors.go) so every candy call site compiles UNCHANGED.
// CheckVerbProvider / CheckContext / the CheckContext scalar types stay in spec/spec
// (checkcontext.go) — the StepKindName cluster is the step-role sibling, imported here alongside
// spec/spec for the *Op the StepProvider / ProvisionActor methods take.
package checkstep

import "github.com/opencharly/spec/spec"

// StepKindName names the TYPED install-plan step a step-providing verb lowers into. The
// host maps it to its internal StepKind enum (kitStepKindToCharly); kept a string so the
// contract need not import charly's package main.
type StepKindName string

const (
	// StepKindServicePackaged — the `service` verb (enable a packaged unit; load-bearing reversals).
	StepKindServicePackaged StepKindName = "service-packaged"
	// StepKindSystemPackages — the `package` verb (install system packages).
	StepKindSystemPackages StepKindName = "system-packages"
)

// ServicePackagedDesc is the candy-decodable construction input for a service-packaged
// step: the host materializer adds the op-resolved scope + candy name and keeps the
// load-bearing Reverse() (disable / restore-enabled / remove-dropin) in package main.
type ServicePackagedDesc struct {
	Unit   string
	Enable bool
}

// SystemPackagesDesc is the candy-decodable construction input for a system-packages step
// (the `package` verb): the authored package name + per-distro map. The host materializer
// resolves the cross-distro name (ResolvePackageName against the image's tags), sets the
// image format + PhaseInstall, and builds the SystemPackagesStep.
type SystemPackagesDesc struct {
	Package    string
	PackageMap map[string]string
}

// StepDescriptor is the candy-decodable construction input for a TYPED install-plan step
// (the build/deploy install timeline). Exactly one variant is non-nil; the host
// materializer rebuilds the real package-main InstallStep from it (computing the
// package-main-only inputs — scope from op.RunAs+img, candy name — and keeping the
// load-bearing Reverse() in package main, so the candy never imports an IR type).
type StepDescriptor struct {
	ServicePackaged *ServicePackagedDesc
	SystemPackages  *SystemPackagesDesc
}

// ResolvePackageName picks the correct package name for the running image's distro: if
// packageMap has a key matching any of the image's distro tags (first match wins — tags
// are authored most-specific-first, "fedora:43" before "fedora"), that mapping is used;
// otherwise the bare pkg name. The single cross-distro name resolver shared by the
// `package` candy's check + act AND the host's step materializer (R3).
func ResolvePackageName(pkg string, packageMap map[string]string, distros []string) string {
	if len(packageMap) == 0 {
		return pkg
	}
	for _, tag := range distros {
		if name, ok := packageMap[tag]; ok && name != "" {
			return name
		}
	}
	return pkg
}

// StepProvider is the OPTIONAL third role of a host-coupled verb candy: a verb whose
// build/deploy ACT lowers into a TYPED install-plan step (service → service-packaged,
// package → system-packages) rather than a shell (ProvisionActor) or a generic OpStep.
// StepKind names the target step (static); ConstructStepDescriptor returns the
// candy-decodable construction inputs for one op. The host wraps a candy implementing
// this in an adapter that satisfies package-main's TypedStepProvider, materializing the
// descriptor into the real IR step.
type StepProvider interface {
	StepKind() StepKindName
	ConstructStepDescriptor(op *spec.Op) StepDescriptor
}

// ProvisionActor is the OPTIONAL second role of a host-coupled verb candy: the do:act
// renderer for a state-provision verb (kernel_param/mount/user/unix_group/file/command/
// service/package), rendering the shell that ENACTS the op under the live init / package
// manager. It is reached at install COMPILE+EMIT (a `run: {plugin: <verb>}` step → the
// build-act RUN in emitTasks, and the local/vm deploy act) AND at runtime act. A candy
// whose verb type implements this ALONGSIDE CheckVerbProvider is registered as a
// multi-role provider (the host adapter then also satisfies the package-main
// ProvisionActor). op is the *spec.Op (the verb's plugin_input rides op.PluginInput);
// distros is the image's distro tag list for package-name resolution. Returns
// (script, ok); ok=false means "no act form for this op" (the host skips/errors per its
// act path). This is the SHELL-string act role — a verb that instead lowers into a typed
// InstallPlan step (service/package) additionally needs the StepProvider contract.
type ProvisionActor interface {
	Reserved() string
	RenderProvisionScript(op *spec.Op, distros []string) (script string, ok bool)
}
