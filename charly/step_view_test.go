package main

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/opencharly/sdk/spec"
)

// TestStepView_RoundTrip is the F2 step-IR faithfulness gate: every concrete InstallStep
// kind must survive the FULL wire path — stepToView → JSON marshal → JSON unmarshal →
// stepFromView — DeepEqual-intact. This proves the serializable per-step IR
// (spec.InstallStepView) drops NO field of any kind, so an external deploy/step plugin
// that EXECUTES the plan walks the SAME data the in-proc DeployTargets walk (R3).
//
// Map fields with `any` values (RawInstallContext/RawStageContext/Repos) are populated
// with JSON-stable values (string/bool/[]any-of-string) — a raw int would deserialize as
// float64 (the standard encoding/json behaviour), which is irrelevant to the converter
// under test and would only muddy the fixture.
func TestStepView_RoundTrip(t *testing.T) {
	cases := []struct {
		name string
		step InstallStep
	}{
		{"SystemPackages", &SystemPackagesStep{
			Format:   "rpm",
			Phase:    PhaseInstall,
			Packages: []string{"vim", "git"},
			Repos:    []RepoSpec{{Raw: map[string]any{"id": "rpmfusion", "url": "https://example/repo"}}},
			Options:  []string{"--nogpgcheck"},
			Copr:     []string{"owner/proj"},
			Modules:  []string{"nodejs:20"},
			Exclude:  []string{"badpkg"},
			Keys:     []string{"ABCDEF"},
			CacheMount: []CacheMountSpec{
				{Dst: "/var/cache/dnf", Sharing: "locked"},
			},
			RawInstallContext: map[string]any{"packages": []any{"vim", "git"}, "noninteractive": true},
		}},
		{"Builder", &BuilderStep{
			Builder:         "pixi",
			BuilderImage:    "fedora-builder:2026.04",
			CandyName:       "python-ml",
			CandyDir:        "/proj/candy/python-ml",
			Phase:           PhaseInstall,
			Artifacts:       []ArtifactRef{{ContainerPath: "/work/.pixi", HostPath: "/home/u/.pixi", Chown: true}},
			RawStageContext: map[string]any{"packages": []any{"numpy"}},
			LocalPkg:        &spec.LocalPkg{PkgGlob: "*.pkg.tar.zst", InstallTemplate: "pacman -U", Probe: "command -v pacman"},
			BuilderDef:      &spec.BuilderDef{ManylinuxFix: "auditwheel repair"},
		}},
		{"Op", &OpStep{
			Op:           &spec.Op{Write: "/etc/marker", Content: "hello\nworld\n", Mode: "0644"},
			CandyName:    "mycandy",
			CandyDir:     "/proj/candy/mycandy",
			CtxPath:      "/proj/candy/mycandy",
			ResolvedUser: "1000",
			To:           "/home/u/marker",
			CandyVars:    map[string]string{"K3D_VERSION": "v5"},
			Distros:      []string{"fedora:43", "fedora"},
		}},
		{"File", &FileStep{
			Source:    "/proj/.build/_inline/x",
			Dest:      "/etc/charly/x.conf",
			Mode:      0o640,
			Owner:     "root",
			CandyName: "mycandy",
		}},
		{"ServicePackaged", &ServicePackagedStep{
			Unit:          "postgresql.service",
			TargetScope:   ScopeUser,
			Enable:        true,
			OverridesText: "[Service]\nEnvironment=X=1\n",
			OverridesPath: "/home/u/.config/systemd/user/postgresql.service.d/charly.conf",
			CandyName:     "postgresql",
			PriorEnabled:  true,
		}},
		{"ServiceCustom", &ServiceCustomStep{
			Name:        "charly-ollama-ollama",
			UnitText:    "[Unit]\nDescription=ollama\n",
			UnitPath:    "/etc/systemd/system/charly-ollama-ollama.service",
			TargetScope: ScopeSystem,
			Enable:      true,
			CandyName:   "ollama",
		}},
		{"ShellHook", &ShellHookStep{
			CandyName: "rust",
			EnvVars:   map[string]string{"CARGO_HOME": "/home/u/.cargo"},
			PathAdd:   []string{"/home/u/.cargo/bin"},
			EnvFile:   "/home/u/.config/opencharly/env.d/rust.env",
		}},
		{"ShellSnippet", &ShellSnippetStep{
			CandyName:   "direnv",
			Origin:      "direnv",
			Shell:       "bash",
			Snippet:     "eval \"$(direnv hook bash)\"\n",
			PathAppend:  []string{"/home/u/.local/bin"},
			Destination: "/home/u/.bashrc",
			Marker:      "direnv",
			UseDropin:   false,
			Priority:    10,
		}},
		{"RepoChange", &RepoChangeStep{
			Format:    "rpm",
			File:      "/etc/yum.repos.d/rpmfusion-free.repo",
			Content:   "[rpmfusion-free]\nname=RPM Fusion\n",
			Checksum:  "deadbeef",
			CandyName: "rpmfusion",
		}},
		{"ApkInstall", &ApkInstallStep{
			Packages:  []spec.ApkPackageSpec{{Package: "com.example.app", Source: "google-play", Arch: "arm64-v8a", AppVersion: "1.2.3"}},
			CandyName: "myapp",
			CandyDir:  "/proj/candy/myapp",
		}},
		{"LocalPkgInstall", &LocalPkgInstallStep{
			PkgbuildRef: "pkg/arch",
			CandyName:   "charly",
			CandyDir:    "/proj/candy/charly",
			ProjectDir:  "/proj",
			Format:      "pac",
			LocalPkg:    &spec.LocalPkg{PkgGlob: "*.pkg.tar.zst", SourceSentinel: "PKGBUILD", BuildTemplate: "makepkg", InstallTemplate: "pacman -U", Probe: "command -v pacman"},
		}},
		{"Reboot", &RebootStep{CandyName: "nvidia-open-dkms"}},
		{"ExternalPlugin", &ExternalPluginStep{
			Op:           &spec.Op{Plugin: "examplestep", PluginInput: map[string]any{"marker": "hello"}},
			CandyName:    "examplestep-consumer",
			ResolvedUser: "root",
			Distros:      []string{"fedora:43"},
		}},
	}

	// Every kind in allStepKinds must be covered — otherwise the round-trip gate has a
	// blind spot for a kind a future cutover adds.
	covered := make(map[StepKind]bool, len(cases))

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			covered[tc.step.Kind()] = true

			view := stepToView(tc.step)

			// The discriminator + the advisory derived fields must reflect the step's
			// own methods (populated once in stepToView; the plugin reads them to pick
			// sudo-vs-user without recomputing the rule).
			if view.Kind != string(tc.step.Kind()) {
				t.Fatalf("Kind discriminator: got %q want %q", view.Kind, tc.step.Kind())
			}
			if view.Scope != tc.step.Scope() {
				t.Fatalf("advisory Scope: got %v want %v", view.Scope, tc.step.Scope())
			}
			if view.Venue != int(tc.step.Venue()) {
				t.Fatalf("advisory Venue: got %d want %d", view.Venue, tc.step.Venue())
			}
			if view.Gate != string(tc.step.RequiresGate()) {
				t.Fatalf("advisory Gate: got %q want %q", view.Gate, tc.step.RequiresGate())
			}

			// FULL wire round-trip: marshal the view to JSON, decode it back, and
			// reconstruct the concrete step.
			raw, err := json.Marshal(view)
			if err != nil {
				t.Fatalf("marshal view: %v", err)
			}
			var decoded spec.InstallStepView
			if err := json.Unmarshal(raw, &decoded); err != nil {
				t.Fatalf("unmarshal view: %v", err)
			}
			got, err := stepFromView(decoded)
			if err != nil {
				t.Fatalf("stepFromView: %v", err)
			}
			if !reflect.DeepEqual(tc.step, got) {
				t.Fatalf("round-trip mismatch\n original: %#v\n got:      %#v", tc.step, got)
			}
		})
	}

	for _, k := range allStepKinds {
		if !covered[k] {
			t.Errorf("step kind %q has no round-trip fixture — every InstallStep kind must be covered", k)
		}
	}
}

// TestStepsToView_PreservesOrder proves the whole-slice converter keeps the plan's
// step ordering (load-bearing for ResolveHome / StepsByVenue and for an executing
// plugin replaying the install timeline).
func TestStepsToView_PreservesOrder(t *testing.T) {
	steps := []InstallStep{
		&ShellHookStep{CandyName: "a"},
		&OpStep{Op: &spec.Op{Mkdir: "/x"}, CandyName: "b"},
		&RebootStep{CandyName: "c"},
	}
	views := stepsToView(steps)
	back, err := stepsFromView(views)
	if err != nil {
		t.Fatalf("stepsFromView: %v", err)
	}
	if len(back) != len(steps) {
		t.Fatalf("length changed: %d -> %d", len(steps), len(back))
	}
	for i := range steps {
		if !reflect.DeepEqual(steps[i], back[i]) {
			t.Fatalf("step %d changed across round-trip:\n %#v\n %#v", i, steps[i], back[i])
		}
	}
}

// TestStepFromView_UnknownKind proves an unrecognized step kind is a hard error, never
// silently dropped (a wire/version contract breach must surface).
func TestStepFromView_UnknownKind(t *testing.T) {
	if _, err := stepFromView(spec.InstallStepView{Kind: "NoSuchKind"}); err == nil {
		t.Fatal("expected an error for an unknown step kind, got nil")
	}
}
