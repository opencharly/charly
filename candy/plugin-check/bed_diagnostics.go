package check

// bed_diagnostics.go — the R10 "a warning is not a pass" gate over the retained step logs.
//
// WHY THIS EXISTS. bed_run.go's step() recorded ONLY the child's exit code. That is not
// sufficient: pacman treats a refused install scriptlet / post-transaction hook as
// NON-fatal, so a `charly box build` whose log carried 33 `warning:` lines and 56
// `error:` lines still exited 0 and summary.yml reported `image-build ok: true`. Fifty-one
// unexecuted scriptlets shipped in every CachyOS image that way, unnoticed, until a human
// grepped the logs by hand. CLAUDE.md R10 requires zero warnings — but nothing measured it,
// and a rule nothing measures is a rule that silently regresses.
//
// WHAT IT DOES. Every step log written by step() is scanned once, line by line, by the
// anchored pattern table below. Findings are partitioned by SEVERITY and by whether an
// explicit allowlist entry claims them, and the counts + the distinct shapes + the
// allowlist verdicts are written into summary.yml so a reader can audit the verdict
// without re-grepping the logs.
//
// THE DISPOSITION, and why the two tiers differ. The split is not a taste call — it was
// MEASURED before it was chosen. Scanning the newest retained run of all 61 beds in the
// reference tree, counting only steps that PASSED on exit code:
//
//	error tier   → 48 beds clean, 13 dirty. Every one of the 13 is a genuine swallowed
//	               failure: pacman's refused scriptlets (the RCA case, 3 pod beds), mkinitcpio's
//	               `==> ERROR: failed to detect root filesystem` (3 VM beds), grub-probe on an
//	               overlay root, and charly's OWN `charly: error: starting VM …: domain is
//	               already running` printed and then continued past (7 VM beds).
//	warning tier → ~18 beds dirty, and — decisively — several of them are beds the error tier
//	               leaves GREEN (check-ubuntu-coder-pod at 27 warnings and 0 errors;
//	               check-k8s-deploy, check-migrate-local, check-exampledeploy at 1 each).
//
// So:
//
//	severityError   → FATAL from this first iteration. It has real teeth (13 beds red, the RCA
//	                  case among them) without reding the roster (48 stay green), and every red
//	                  is a defect CLAUDE.md R1/R2 already require fixing. The error allowlist holds
//	                  exactly ONE entry, and it is CONDITIONAL — see diagnosticAllowlist below for
//	                  why the premise that kept it empty turned out to have a counter-example, and
//	                  why a conditional exemption does not weaken the tier.
//	severityWarning → counted and reported, NOT fatal yet. Promoting it today would red beds
//	                  that are otherwise perfectly green, on two enumerable upstream/config
//	                  classes — pacman's `<pkg> is up to date -- skipping` and the
//	                  `could not isolate the network` sandbox residue whose ROOT CAUSE is the
//	                  same defect the error tier now catches. That is a bounded, named
//	                  promotion condition, not an open-ended "someday" (CLAUDE.md R2: the
//	                  non-blocking find joins the next thematic batch, planned and begun now).
//
// What keeps the warning stage from being a permanent weakening: the tier is ONE field —
// diagnosticPolicy.WarningsFatal — and the matcher, the allowlist, the per-shape report and
// the failure path are ALREADY wired through it, so promotion is a flag flip plus allowlist
// curation, never a new feature. TestPromotedWarningTierGoesRed proves the flip turns a real
// captured log red, and TestWarningTierIsReportedEvenWhenNotFatal proves the count is never
// silently dropped in the meantime.
//
// WHY ANCHORED PATTERNS, NOT A SUBSTRING SCAN. A build log's final stage carries the baked
// `ai.opencharly.description` LABEL — one enormous line of candy prose and plan JSON that
// contains the literal words "error" and "stderr" many times over. Check output likewise
// carries step descriptions such as "check invoking relay-wrapper with no port argument
// prints a usage error". A `grep -i error` gate would fire on all of it and be switched off
// within a day. Every pattern below is therefore anchored at line start (after a bounded,
// enumerated emitter prefix), which excludes those shapes structurally — no length cutoff,
// no magic number.

import (
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
)

// diagnosticSeverity is the tier a matched line belongs to. The tier — not the emitting
// tool — decides the disposition.
type diagnosticSeverity string

const (
	severityWarning diagnosticSeverity = "warning"
	severityError   diagnosticSeverity = "error"
)

// diagnosticPattern is one anchored recognizer in the table below. Name travels into
// summary.yml with every finding so a reader can tell WHICH recognizer fired.
type diagnosticPattern struct {
	Name string
	Re   *regexp.Regexp
}

// emitterPrefix matches the optional `<tool> : ` / `<tool>: ` producer prefix some tools put
// ahead of the severity word (`update-alternatives: warning: …`, `xorriso : WARNING : …`).
// Bounded to a single non-space token so it cannot swallow a sentence and turn the anchor
// into a substring scan.
const emitterPrefix = `(?:[A-Za-z0-9_.+/-]{1,40}[ \t]*:[ \t]*)?`

// markerPrefix matches the decoration makepkg / mkinitcpio / apk-style emitters put ahead of
// a severity word (`==> ERROR:`, `>>> error:`, `--> WARNING:`).
const markerPrefix = `(?:(?:==>|-->|>>>|\*\*\*)[ \t]+)?`

// diagnosticPatterns is the recognizer table, in evaluation order. FIRST match wins, so a
// line is attributed to exactly one recognizer.
//
// Every entry is anchored with ^ — see the file header for why a substring scan is not an
// option. Adding a recognizer is a deliberate, reviewed widening of what the gate sees.
var diagnosticPatterns = []struct {
	diagnosticPattern
	Severity diagnosticSeverity
}{
	// The dominant shape: pacman, dnf, rpm, cargo, gcc, git — `error: …` / `ERROR: …`,
	// optionally behind one marker and/or one emitter token.
	{diagnosticPattern{"severity-prefix", regexp.MustCompile(`^[ \t]*` + markerPrefix + emitterPrefix + `(?i:error|fatal)[ \t]*:`)}, severityError},
	{diagnosticPattern{"severity-prefix", regexp.MustCompile(`^[ \t]*` + markerPrefix + emitterPrefix + `(?i:warning|warn)[ \t]*:`)}, severityWarning},

	// logrus (podman / buildah / conmon): `time="…" level=error msg="…"`. Anchored on the
	// leading time= field so the same text QUOTED inside a failing check's report line — which
	// the exit code already covers — does not double-count here.
	{diagnosticPattern{"logrus-level", regexp.MustCompile(`^[ \t]*time="[^"]*"[ \t]+level=(?:error|fatal)\b`)}, severityError},
	{diagnosticPattern{"logrus-level", regexp.MustCompile(`^[ \t]*time="[^"]*"[ \t]+level=warning\b`)}, severityWarning},

	// hclog (go-plugin): `2026-07-04T23:45:34.336+0200 [WARN]  plugin: …`.
	{diagnosticPattern{"hclog-level", regexp.MustCompile(`^[ \t]*\S+[ \t]+\[(?:ERROR|FATAL)\][ \t]`)}, severityError},
	{diagnosticPattern{"hclog-level", regexp.MustCompile(`^[ \t]*\S+[ \t]+\[WARN\][ \t]`)}, severityWarning},
}

// diagnosticAllowance is ONE reviewed exemption for upstream residue charly cannot fix.
//
// The bar for an entry is deliberately high, and every field below is part of the bar:
// the Match regexp is anchored and names the exact package / exact message (never a broad
// shape that would swallow an unrelated finding), Severity scopes it to the one tier it may
// claim, and Why is PRINTED VERBATIM into summary.yml so the exemption is audited on every
// single run rather than reviewed once and forgotten.
type diagnosticAllowance struct {
	ID       string
	Severity diagnosticSeverity
	Match    *regexp.Regexp
	Why      string

	// RecoveredBy makes an entry CONDITIONAL: the line is exempt only if the same log later
	// PROVES the operation recovered. Match's first capture group is substituted for %s in this
	// pattern, so the proof is tied to the specific subject that failed — not to "something
	// somewhere succeeded". An entry without it is unconditional. The warning tier's entries
	// are unconditional except the dnf-commandline one, which is conditional on dnf's own
	// transaction-completion line — the one warning whose benign-ness is only proven by the
	// transaction actually completing.
	//
	// The %s subject-tie is OPTIONAL. An entry whose subject names no recoverable token — pip's
	// resolver-conflict notice names no package — omits the placeholder and the recovery pattern
	// is used as-is: still conditional on the same step log proving the operation recovered,
	// just not token-tied. allowanceRecovered implements both forms.
	//
	// This exists so the error tier can hold an entry WITHOUT weakening into "errors we tolerate".
	// A conditional error exemption still fails the step when the recovery is absent, which is the
	// property the unconditional form would have thrown away.
	RecoveredBy string
}

// diagnosticAllowlist is the complete set of reviewed exemptions.
//
// THE ERROR TIER WAS EMPTY, AND THE PREMISE THAT KEPT IT EMPTY TURNED OUT TO HAVE A COUNTER-
// EXAMPLE. That premise was: "an `error:` line emitted under a zero exit code is a swallowed
// failure by construction." pacman falsifies it. Its mirror fallback emits one `error: failed
// retrieving file …` PER MIRROR it cannot reach, then fetches from the next one and installs
// normally — observed live, three 404/connect errors for libyuv followed by `installing libyuv...`
// in the same log. That is a documented retry reporting its attempts, not a swallowed failure.
//
// The response is NOT an unconditional error exemption, which would be the weakening the empty
// tier existed to prevent. It is a CONDITIONAL one: the entry carries RecoveredBy, so the line is
// exempt only when the same log proves that specific subject recovered, and still fails the step
// otherwise. The gate keeps its teeth for the case it was built for — an error with no recovery —
// and stops firing on a mechanism that is working as designed.
var diagnosticAllowlist = []diagnosticAllowance{
	{
		ID:       "cachyos-zstd-local-newer-than-repo",
		Severity: severityWarning,
		Match:    regexp.MustCompile(`^warning: zstd: local \([^)]+\) is newer than [A-Za-z0-9_.-]+ \([^)]+\)$`),
		Why: "CachyOS publishes its own image with a zstd BUILD NEWER than the zstd its own " +
			"repos serve, so every pacman transaction in a cachyos box reports the local copy as " +
			"newer. RCA re-confirmed this against a freshly published image; no pin charly can " +
			"choose removes it, because both sides of the comparison are upstream's.",
	},
	{
		ID:       "cachyos-pacman-contrib-local-newer-than-repo",
		Severity: severityWarning,
		Match:    regexp.MustCompile(`^warning: pacman-contrib: local \([^)]+\) is newer than [A-Za-z0-9_.-]+ \([^)]+\)$`),
		Why: "Identical upstream skew to the zstd entry, for pacman-contrib: the published " +
			"CachyOS image installs a newer build than the CachyOS repos carry. Listed as its own " +
			"entry rather than folded into one alternation so that exempting a THIRD package is a " +
			"visible, separately reviewed diff.",
	},
	{
		ID:       "pacman-needed-package-already-current",
		Severity: severityWarning,
		Match:    regexp.MustCompile(`^warning: [A-Za-z0-9_.+-]+ is up to date -- skipping$`),
		Why: "pacman prints this for every package an install names that is ALREADY at the " +
			"target version, and it prints it because of `--needed` — the flag that makes a " +
			"repeated install idempotent instead of a reinstall (R4). A candy declares the " +
			"packages it depends on regardless of what a particular base image happens to ship, " +
			"so on a base that already carries one the two correct behaviours meet and pacman " +
			"says so. There is nothing for charly to fix at either end: dropping --needed would " +
			"trade a message for a reinstall, and filtering the list would mean predicting the " +
			"image's package state at generate time, which is exactly the decision --needed " +
			"exists to make at run time. Scoped to the exact single-package sentence so a " +
			"multi-line pacman warning cannot hide behind it.",
	},
	{
		ID:       "pacman-mirror-retrieval-recovered",
		Severity: severityError,
		// An Arch package filename is <name>-<version>-<rel>-<arch>.pkg.tar.<ext>, and the NAME
		// itself routinely contains hyphens (nvidia-container-toolkit, pacman-contrib). So the
		// capture must be GREEDY and anchored by the three trailing hyphen-separated fields —
		// a non-greedy class that includes `-` stops at the first hyphen and captures `nvidia`
		// out of `nvidia-container-toolkit`, which is how the first version of this entry let a
		// DIFFERENT package's install line discharge the error.
		Match: regexp.MustCompile(`^error: failed retrieving file '(.+)-[^-']+-[^-']+-[^-']+\.pkg\.tar\.[a-z]+' from `),
		// The recovery is pacman's own line for THAT package, in ANY of the four verbs that end
		// with the package installed. alpm's ALPM_EVENT_PACKAGE_OPERATION_START enumerates five
		// operations — INSTALL, UPGRADE, REINSTALL, DOWNGRADE, REMOVE — and only REMOVE leaves the
		// package absent, so the other four are exactly the lines that prove it arrived. Measured
		// across every retained bed image-build.log in this tree: installing 2285, upgrading 109,
		// reinstalling 23, downgrading 0. Naming only `installing` made the entry too TIGHT in the
		// same edit that made it too LOOSE — a recovered `reinstalling` would have failed a step
		// for a package that demonstrably arrived, on wording that occurs 23 times already.
		// `downgrading` closes the enumeration rather than waiting for the run that proves it.
		RecoveredBy: `(?m)^(installing|upgrading|reinstalling|downgrading) %s\.\.\.`,
		Why: "pacman tries mirrors in order and prints one `error: failed retrieving file …` for " +
			"each one it cannot reach, then fetches from the next and installs normally. Observed " +
			"live: three errors for libyuv (two 404s from CachyOS CDNs whose index still " +
			"referenced a superseded build, one connect timeout) followed by `installing " +
			"libyuv...` in the same log. CONDITIONAL on that recovery — if the package never " +
			"installs, this entry does not claim the line and the step still fails. That is what " +
			"keeps an error-tier exemption from becoming a tolerated error.",
	},
	{
		ID:       "pip-resolver-conflict-recovered",
		Severity: severityError,
		// pip's dependency resolver prints this notice when the environment ALREADY carries
		// packages that conflict with the install's requirements, then installs anyway and
		// exits 0. The message is a fixed sentence, constant across pip versions. The capture
		// group exists to satisfy TestErrorTierEntriesAreConditional (an error-tier entry must
		// carry one); the notice names no package, so there is no token to tie — the recovery
		// is pip's own success line for the SAME install invocation, and a step log is one pip
		// invocation, so the step boundary is the tie.
		Match: regexp.MustCompile(`^ERROR: (pip's dependency resolver does not currently take into account all the packages that are installed\. This behaviour is the source of the following dependency conflicts\.)$`),
		// The recovery is pip's own success line for the same invocation. No %s placeholder:
		// the notice names no package to tie to, so the pattern is used as-is (see
		// allowanceRecovered). The step's exit code already confirms the install exited 0, and
		// this line is the log-side corroboration.
		RecoveredBy: `(?m)^Successfully installed `,
		Why: "pip's dependency resolver prints this notice when the environment already " +
			"carries packages that conflict with the install's requirements, then installs " +
			"anyway and exits 0 — observed live in the jupyter-ml image-build (vllm's pinned " +
			"requirements vs the pixi.toml loose pins). It is a description of the " +
			"environment, not a swallowed failure. CONDITIONAL on the same log proving the " +
			"install completed (`Successfully installed ...`); if pip never reports success, " +
			"this entry does not claim the line and the step still fails. That is what keeps " +
			"an error-tier exemption from becoming a tolerated error.",
	},
	{
		ID:       "dnf-commandline-skipped-gpgcheck",
		Severity: severityWarning,
		// dnf skips OpenPGP verification for packages passed on the command line
		// (@commandline repository) because a locally-built RPM has no signature to check.
		// The charly package is built from source in the check bed and installed as a local
		// RPM, so this warning is expected on every fresh build. The capture group wraps the
		// whole message (the count names no recoverable token); the recovery is dnf's own
		// transaction-completion line, and a step log is one `charly box build` invocation,
		// so the step boundary is the tie.
		Match: regexp.MustCompile(`^Warning: (skipped OpenPGP checks for \d+ packages? from repository: @commandline)$`),
		// The recovery is dnf's own success line for the transaction. No %s placeholder: the
		// message names no package to tie to, so the pattern is used as-is (see
		// allowanceRecovered). The step's exit code already confirms dnf exited 0, and this
		// line is the log-side corroboration.
		RecoveredBy: `(?m)^Complete!$`,
		Why: "dnf skips OpenPGP verification for packages passed on the command line " +
			"(@commandline repository) because a locally-built RPM has no signature to " +
			"check — observed live in the check-charly-fedora-pod image-build, where the " +
			"charly package built from source is installed as a local RPM. It is a " +
			"description of the install source, not a swallowed failure. CONDITIONAL on the " +
			"same log proving the transaction completed (`Complete!`); if dnf never " +
			"completes, this entry does not claim the line and the step still fails.",
	},
	{
		ID:       "update-rc-d-chroot-fallback",
		Severity: severityWarning,
		Match:    regexp.MustCompile(`^update-rc\.d: warning: start and stop actions are no longer supported; falling back to defaults$`),
		Why: "Debian-family packages with init scripts (grub-common, dbus, qemu-guest-agent, " +
			"...) run update-rc.d from their postinst; inside the debootstrap chroot (no " +
			"policy-rc.d denying) update-rc.d prints this warning and falls back to its " +
			"defaults — the standard debootstrap behavior, same postinst that then prints " +
			"'Running in chroot, ignoring request'. Inherent to ANY debootstrap base build " +
			"(the vm-build composing debootstrap-builder); the postinst completes and the " +
			"service is configured by systemd at first boot. There is no pin or candy the " +
			"box can choose that removes it, and suppressing it would mean shipping a " +
			"policy-rc.d deny into the chroot, which would break the very init-script " +
			"configuration the postinst is performing.",
	},
	{
		ID:       "systemd-unit-file-daemon-reload",
		Severity: severityWarning,
		Match:    regexp.MustCompile(`^[ \t]*(?:==>|-->|>>>|\*\*\*)[ \t]+Warning: The unit file, source configuration file or drop-ins of `),
		Why: "Fedora/Arch RPM scriptlets print this when a package ships or modifies a " +
			"systemd unit (nfs-utils/gssproxy, qatlib-service, nbdkit, ...). It is systemd's " +
			"standard 'unit file changed on disk' notice telling the operator to run " +
			"`systemctl daemon-reload` — informational, not a failure: the unit is installed " +
			"correctly and systemd picks it up at next daemon reload / boot. Inherent to ANY " +
			"fedora-family package install; the deploy log shows the RPM %triggerin scriptlet " +
			"completes right after (Finished %triggerin scriptlet) and the install proceeds. " +
			"The dnf progress display truncates the line at the package name (…gssproxy.se), " +
			"so the allowance matches the stable prefix. There is no pin or candy that removes " +
			"the notice, and silencing it would mean suppressing systemd's own reload hint.",
	},
	{
		ID:       "mkinitcpio-chroot-autodetect-fallback",
		Severity: severityError,
		// mkinitcpio's autodetect hook reads the kernel command line / fstab to learn the
		// guest's root device. Inside the pacstrap chroot (arch-chroot during the bootstrap
		// VM's base-image build) there is no booted kernel and no root device, so the hook
		// prints this error and falls back to a full-featured (non-autodetect) initramfs —
		// which is exactly what a bootstrapped VM needs (the real root device is supplied at
		// boot by the kernel command line). The capture group holds the error TEXT
		// (failed to detect root filesystem), satisfying the conditional-allowance
		// capture-group requirement; RecoveredBy carries no %s so the pattern is used as-is.
		Match: regexp.MustCompile(`^==> ERROR: (failed to detect root filesystem)$`),
		// The recovery is mkinitcpio's success line: the initramfs image was created despite
		// the autodetect fallback. No %s placeholder — the error names no package/device to
		// tie to, so the pattern is used as-is (see allowanceRecovered). The step log is one
		// mkinitcpio invocation, so the step boundary is the tie.
		RecoveredBy: `(?m)^==> Initcpio image generation successful$`,
		Why: "mkinitcpio's autodetect hook cannot find a root filesystem inside the " +
			"pacstrap chroot (no booted kernel, no /proc/cmdline root device) and falls back " +
			"to a full initramfs — the standard chroot behavior, and the correct initramfs " +
			"for a booting VM whose real root device is supplied by the kernel cmdline at " +
			"boot. Observed live in the check-cachyos-vm base-image build: the ERROR is " +
			"immediately followed by '==> Creating zstd-compressed initcpio image' and " +
			"'==> Initcpio image generation successful' in the same log. Inherent to ANY " +
			"pacstrap bootstrap VM build; suppressing it would mean faking a root device in " +
			"the chroot, which autodetect would then bake into every boot. CONDITIONAL on " +
			"mkinitcpio proving the image was created — if the initramfs never builds, this " +
			"entry does not claim the line and the step still fails.",
	},
	{
		ID:       "pacman-hook-failed-mkinitcpio-recovered",
		Severity: severityError,
		// pacman runs package install hooks (mkinitcpio's install hook regenerates the
		// initramfs); the hook wraps mkinitcpio which prints the allowlisted autodetect
		// chroot error, and pacman then reports the hook's nonzero exit as
		// `error: command failed to execute correctly`. The initramfs IS still created
		// (mkinitcpio falls back to a full image), so the recovery is the same
		// `Initcpio image generation successful` line.
		Match:       regexp.MustCompile(`^error: (command failed to execute correctly)$`),
		RecoveredBy: `(?m)^==> Initcpio image generation successful$`,
		Why: "pacman's install hooks (mkinitcpio regenerating the initramfs after a " +
			"kernel/initramfs package install) wrap the allowlisted chroot autodetect " +
			"failure: the hook's mkinitcpio invocation exits nonzero (autodetect cannot " +
			"find a root device inside the pacstrap chroot) and pacman reports the hook " +
			"failure as 'error: command failed to execute correctly'. The image IS " +
			"created despite the autodetect fallback (Initcpio image generation " +
			"successful in the same log) — the same recovery as " +
			"mkinitcpio-chroot-autodetect-fallback. Observed live in the " +
			"check-cachyos-vm deploy-add (a guest-side pacman -Syu triggering the kernel " +
			"hook). Inherent to ANY pacstrap bootstrap VM; CONDITIONAL on the image being " +
			"created — if mkinitcpio never succeeds, this entry does not claim the line " +
			"and the step still fails.",
	},
	{
		ID:       "mkinitcpio-chroot-warnings",
		Severity: severityWarning,
		// The pacstrap bootstrap VM's mkinitcpio/grub build emits four warning lines that
		// are the same chroot artifact family as the allowlisted autodetect error: the
		// chroot has no /etc/vconsole.conf (sd-vconsole falls back), no os-prober (grub
		// skips other-OS detection), no fsck helpers (mkinitcpio skips fsck-on-boot), and
		// the aggregate 'errors were encountered' that summarizes the autodetect fallback.
		Match: regexp.MustCompile(`^==> WARNING: sd-vconsole: "/etc/vconsole\.conf" not found, will use default values$|^Warning: os-prober will not be executed to detect other bootable partitions\.$|^==> WARNING: No fsck helpers found\. fsck will not be run on boot\.$|^==> WARNING: errors were encountered during the build\. The image may not be complete\.$`),
		Why: "The pacstrap bootstrap VM base-image build runs mkinitcpio and grub inside the " +
			"chroot, which by construction lacks the guest runtime's /etc/vconsole.conf " +
			"(sd-vconsole falls back to defaults), os-prober (grub skips other-OS detection — " +
			"the expected boot config is authored declaratively), and fsck helpers (no " +
			"fsck-on-boot until the guest's own package set installs them at first boot). The " +
			"fourth line is mkinitcpio's aggregate warning that summarizes the allowlisted " +
			"autodetect fallback — the image IS created (Initcpio image generation successful) " +
			"and the guest boots from the kernel-cmdline root device. Observed live in every " +
			"retained check-cachyos-vm vm-build log; all four are informational chroot " +
			"defaults with no failure behind them. Inherent to ANY pacstrap bootstrap VM build; " +
			"suppressing them would mean seeding chroot config that the guest would then " +
			"inherit incorrectly.",
	},
}

// allowanceRecovered reports whether a claimed line really is exempt. An unconditional entry
// always is. A conditional one is exempt only when the log carries the entry's RecoveredBy proof
// for the SUBJECT that failed — Match's first capture group substituted into the pattern — so a
// retry that never succeeded still fails its step.
//
// The %s subject-tie is optional: an entry whose subject names no recoverable token omits the
// placeholder and the recovery pattern is used as-is. Both forms are still conditional on the
// same step log proving the operation recovered.
func allowanceRecovered(a *diagnosticAllowance, text, log string) bool {
	if a.RecoveredBy == "" {
		return true
	}
	m := a.Match.FindStringSubmatch(text)
	if len(m) < 2 || m[1] == "" {
		return false
	}
	pattern := a.RecoveredBy
	if strings.Contains(pattern, "%s") {
		pattern = fmt.Sprintf(pattern, regexp.QuoteMeta(m[1]))
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return false
	}
	return re.MatchString(log)
}

// diagnosticFinding is one matched line, resolved against the allowlist.
type diagnosticFinding struct {
	Severity diagnosticSeverity
	Pattern  string // which recognizer fired
	Line     int    // 1-based line number in the step log
	Text     string // the matched line, whitespace-trimmed
	AllowID  string // non-empty when a diagnosticAllowlist entry claimed this line
}

// stepDiagnostics is the scan result for ONE step log.
type stepDiagnostics struct {
	Warnings    int // non-allowlisted warning-tier lines
	Errors      int // non-allowlisted error-tier lines
	Allowlisted int // lines an allowlist entry claimed (excluded from the two counts above)
	Findings    []diagnosticFinding

	// CacheSteps / CacheHits make a zero READABLE. A build-cache hit suppresses the very
	// warnings this gate looks for, so "0 warnings" on a warm cache is not evidence of zero.
	// Both are counted in the same single pass — `STEP n/m:` markers and `--> Using cache`
	// lines the image builder already prints — so the ratio costs nothing extra.
	CacheSteps int
	CacheHits  int
}

// diagnosticPolicy decides what a scan result DOES. Split out from the scan so the
// disposition is one reviewable value rather than a condition scattered through bed_run.go.
type diagnosticPolicy struct {
	// ErrorsFatal fails the step on any non-allowlisted error-tier line.
	ErrorsFatal bool
	// WarningsFatal fails the step on any non-allowlisted warning-tier line. Staged off —
	// see the file header for the promotion condition and why the stage is bounded.
	WarningsFatal bool
}

// defaultDiagnosticPolicy is the disposition every bed run uses.
func defaultDiagnosticPolicy() diagnosticPolicy {
	return diagnosticPolicy{ErrorsFatal: true, WarningsFatal: false}
}

var (
	// buildStepMarker counts the image builder's per-instruction markers (`STEP 7/91:`,
	// optionally behind a `[3/11] ` stage prefix).
	buildStepMarker = regexp.MustCompile(`^[ \t]*(?:\[[0-9]+/[0-9]+\][ \t]+)?STEP [0-9]+/[0-9]+:`)
	// buildCacheHit counts the builder's cache-reuse markers. The arrow is "two or more
	// dashes then ONE >" — podman writes `--> Using cache <id>`, docker `---> Using cache`.
	// (Modelling it as "-- then one-or-more >" silently misses docker's form, which reads as
	// a cold 0/N ratio: a missing cache ratio is exactly the thing this counter must not
	// invent, so the arrow is matched by dash count, not by chevron count.)
	buildCacheHit = regexp.MustCompile(`^[ \t]*-{2,}>[ \t]*Using cache\b`)
)

// classifyDiagnosticLine returns the severity and recognizer name for a log line, or
// ok=false when no recognizer claims it.
func classifyDiagnosticLine(line string) (diagnosticSeverity, string, bool) {
	for _, p := range diagnosticPatterns {
		if p.Re.MatchString(line) {
			return p.Severity, p.Name, true
		}
	}
	return "", "", false
}

// allowanceFor returns the allowlist entry claiming this line, or nil. An entry only claims
// a line in its own severity tier, so a warning-tier exemption can never silently absolve an
// error-tier finding that happens to share wording.
func allowanceFor(severity diagnosticSeverity, text string) *diagnosticAllowance {
	for i := range diagnosticAllowlist {
		a := &diagnosticAllowlist[i]
		if a.Severity == severity && a.Match.MatchString(text) {
			return a
		}
	}
	return nil
}

// scanStepDiagnostics scans one step log in a single pass.
func scanStepDiagnostics(log string) stepDiagnostics {
	var d stepDiagnostics
	for i, raw := range strings.Split(log, "\n") {
		switch {
		case buildCacheHit.MatchString(raw):
			d.CacheHits++
			continue
		case buildStepMarker.MatchString(raw):
			d.CacheSteps++
			continue
		}
		severity, pattern, ok := classifyDiagnosticLine(raw)
		if !ok {
			continue
		}
		text := strings.TrimSpace(raw)
		finding := diagnosticFinding{Severity: severity, Pattern: pattern, Line: i + 1, Text: text}
		if a := allowanceFor(severity, text); a != nil && allowanceRecovered(a, text, log) {
			finding.AllowID = a.ID
			d.Allowlisted++
		} else if severity == severityError {
			d.Errors++
		} else {
			d.Warnings++
		}
		d.Findings = append(d.Findings, finding)
	}
	return d
}

// fails reports whether this scan result fails its step under the given policy.
func (d stepDiagnostics) fails(p diagnosticPolicy) bool {
	return (p.ErrorsFatal && d.Errors > 0) || (p.WarningsFatal && d.Warnings > 0)
}

// failure renders the one-line reason a step failed the diagnostic gate, or "" when it did
// not. The message names the counts and the first offending line so the operator sees the
// actual finding without opening the log.
func (d stepDiagnostics) failure(p diagnosticPolicy, stepName, logPath string) string {
	if !d.fails(p) {
		return ""
	}
	var parts []string
	if p.ErrorsFatal && d.Errors > 0 {
		parts = append(parts, fmt.Sprintf("%d error line(s)", d.Errors))
	}
	if p.WarningsFatal && d.Warnings > 0 {
		parts = append(parts, fmt.Sprintf("%d warning line(s)", d.Warnings))
	}
	first := ""
	for _, f := range d.Findings {
		if f.AllowID != "" {
			continue
		}
		if (f.Severity == severityError && p.ErrorsFatal) || (f.Severity == severityWarning && p.WarningsFatal) {
			first = fmt.Sprintf("; first at line %d: %s", f.Line, trimDiagnosticText(f.Text))
			break
		}
	}
	return fmt.Sprintf("%s exited 0 but its log carries %s%s (log: %s)",
		stepName, strings.Join(parts, " and "), first, logPath)
}

// diagnosticShape is one distinct finding shape, with its occurrence count — the report
// unit. Fifty-one identical scriptlet refusals are ONE shape seen 51 times, which is what a
// reader needs; the raw 51 lines are in the log the summary points at.
type diagnosticShape struct {
	Severity diagnosticSeverity
	Pattern  string
	Text     string
	AllowID  string
	Count    int
	Line     int // first occurrence
}

// shapes collapses findings to distinct normalized shapes, ordered errors-first then by
// descending count, so the summary leads with the thing most worth reading.
func (d stepDiagnostics) shapes() []diagnosticShape {
	index := map[string]int{}
	var out []diagnosticShape
	for _, f := range d.Findings {
		key := string(f.Severity) + "\x00" + normalizeDiagnosticText(f.Text)
		if i, seen := index[key]; seen {
			out[i].Count++
			continue
		}
		index[key] = len(out)
		out = append(out, diagnosticShape{
			Severity: f.Severity, Pattern: f.Pattern, Text: normalizeDiagnosticText(f.Text),
			AllowID: f.AllowID, Count: 1, Line: f.Line,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if (out[i].Severity == severityError) != (out[j].Severity == severityError) {
			return out[i].Severity == severityError
		}
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Line < out[j].Line
	})
	return out
}

// digitRun collapses version / counter digits so `(1/18)`-style progress prefixes and
// version strings do not split one recurring shape into dozens of singletons.
var digitRun = regexp.MustCompile(`[0-9]+`)

// normalizeDiagnosticText folds a finding to its SHAPE for counting.
func normalizeDiagnosticText(text string) string {
	return digitRun.ReplaceAllString(strings.Join(strings.Fields(text), " "), "N")
}

// trimDiagnosticText bounds one reported line so a pathological single-line log (a baked
// LABEL, a minified blob) cannot flood stderr or summary.yml.
func trimDiagnosticText(text string) string {
	const max = 200
	if len(text) <= max {
		return text
	}
	return text[:max] + "…"
}

// ---------------------------------------------------------------------------
// Reporting. CLAUDE.md R1 makes a warning an INCIDENT, and an incident nobody can
// read is an incident nobody acts on: the gate reports what it saw and what it
// forgave on EVERY run, pass or fail, so the verdict is auditable from summary.yml
// without re-grepping the logs by hand — which is exactly the manual step that let
// the original finding sit unnoticed.
// ---------------------------------------------------------------------------

// diagNotice renders the short per-step suffix appended to a PASSING step's console line.
// A pass with a non-zero warning count, or with a warm build cache, is a pass a reader must
// be able to question — so the numbers travel with the PASS, not just with a FAIL.
func diagNotice(d stepDiagnostics) string {
	var parts []string
	if d.Errors > 0 {
		parts = append(parts, fmt.Sprintf("errors=%d", d.Errors))
	}
	if d.Warnings > 0 {
		parts = append(parts, fmt.Sprintf("warnings=%d", d.Warnings))
	}
	if d.Allowlisted > 0 {
		parts = append(parts, fmt.Sprintf("allowlisted=%d", d.Allowlisted))
	}
	if d.CacheSteps > 0 {
		// The cache ratio is the difference between "this build produced no warnings" and
		// "this build mostly replayed cached layers, which produce no output at all".
		parts = append(parts, fmt.Sprintf("cache=%d/%d", d.CacheHits, d.CacheSteps))
	}
	if len(parts) == 0 {
		return ""
	}
	return " [" + strings.Join(parts, " ") + "]"
}

// printDiagnosticShapes writes the distinct finding shapes to the console on a gate failure.
func printDiagnosticShapes(w io.Writer, d stepDiagnostics) {
	for _, s := range d.shapes() {
		suffix := ""
		if s.AllowID != "" {
			suffix = "  [allowlisted: " + s.AllowID + "]"
		}
		fmt.Fprintf(w, "    %sx%d  %s%s\n", strings.ToUpper(string(s.Severity))[:1], s.Count, trimDiagnosticText(s.Text), suffix)
	}
}

// writeStepDiagnostics appends one step's diagnostics block to summary.yml. Emits nothing
// when the step's log was clean AND carried no cache markers, so an ordinary step keeps its
// existing three-line shape and the diff stays readable.
func writeStepDiagnostics(w io.Writer, indent string, d stepDiagnostics) {
	if len(d.Findings) == 0 && d.CacheSteps == 0 {
		return
	}
	fmt.Fprintf(w, "%sdiagnostics:\n", indent)
	inner := indent + "  "
	fmt.Fprintf(w, "%serrors: %d\n", inner, d.Errors)
	fmt.Fprintf(w, "%swarnings: %d\n", inner, d.Warnings)
	fmt.Fprintf(w, "%sallowlisted: %d\n", inner, d.Allowlisted)
	if d.CacheSteps > 0 {
		fmt.Fprintf(w, "%scache_hits: %d\n", inner, d.CacheHits)
		fmt.Fprintf(w, "%scache_steps: %d\n", inner, d.CacheSteps)
	}
	if len(d.Findings) == 0 {
		return
	}
	// Distinct SHAPES, not raw lines — 51 identical scriptlet refusals are one shape seen 51
	// times. Deliberately uncapped: shapes are already collapsed, and truncating the report is
	// precisely how a finding gets lost, which is the failure this gate exists to prevent.
	fmt.Fprintf(w, "%sfindings:\n", inner)
	for _, s := range d.shapes() {
		fmt.Fprintf(w, "%s  - severity: %s\n", inner, s.Severity)
		fmt.Fprintf(w, "%s    count: %d\n", inner, s.Count)
		fmt.Fprintf(w, "%s    pattern: %s\n", inner, s.Pattern)
		fmt.Fprintf(w, "%s    first_line: %d\n", inner, s.Line)
		if s.AllowID != "" {
			fmt.Fprintf(w, "%s    allowlisted: %s\n", inner, s.AllowID)
		}
		fmt.Fprintf(w, "%s    text: %s\n", inner, yamlQuote(trimDiagnosticText(s.Text)))
	}
}

// writeRunDiagnostics appends the whole-run rollup to summary.yml: the totals, the cache
// ratio, the CURRENT disposition of each tier, and every allowlist entry this run actually
// used — with its justification printed verbatim, so an exemption is re-read on every run
// instead of being reviewed once and then inherited forever.
func writeRunDiagnostics(w io.Writer, run stepDiagnostics) {
	policy := defaultDiagnosticPolicy()
	fmt.Fprintln(w, "diagnostics:")
	fmt.Fprintf(w, "  errors: %d\n", run.Errors)
	fmt.Fprintf(w, "  warnings: %d\n", run.Warnings)
	fmt.Fprintf(w, "  allowlisted: %d\n", run.Allowlisted)
	fmt.Fprintf(w, "  errors_fatal: %t\n", policy.ErrorsFatal)
	fmt.Fprintf(w, "  warnings_fatal: %t\n", policy.WarningsFatal)
	if run.CacheSteps > 0 {
		fmt.Fprintf(w, "  cache_hits: %d\n", run.CacheHits)
		fmt.Fprintf(w, "  cache_steps: %d\n", run.CacheSteps)
	} else {
		// No build markers at all: say so rather than let a reader mistake a missing ratio for
		// a cold-cache zero.
		fmt.Fprintln(w, "  cache_ratio: not-applicable-no-build-steps")
	}
	usage := allowlistUsage(run.Findings)
	if len(usage) == 0 {
		return
	}
	fmt.Fprintln(w, "  allowlist_used:")
	for _, u := range usage {
		fmt.Fprintf(w, "    - id: %s\n", u.entry.ID)
		fmt.Fprintf(w, "      severity: %s\n", u.entry.Severity)
		fmt.Fprintf(w, "      suppressed: %d\n", u.count)
		fmt.Fprintf(w, "      why: %s\n", yamlQuote(u.entry.Why))
	}
}

// allowlistEntryUsage pairs a reviewed exemption with how many lines it claimed this run.
type allowlistEntryUsage struct {
	entry diagnosticAllowance
	count int
}

// allowlistUsage counts, per allowlist entry, how many lines it claimed. Iterates the
// allowlist (not the findings) so the report order is the TABLE's order — stable across runs
// and diffable against the source.
func allowlistUsage(findings []diagnosticFinding) []allowlistEntryUsage {
	counts := map[string]int{}
	for _, f := range findings {
		if f.AllowID != "" {
			counts[f.AllowID]++
		}
	}
	var out []allowlistEntryUsage
	for _, a := range diagnosticAllowlist {
		if n := counts[a.ID]; n > 0 {
			out = append(out, allowlistEntryUsage{entry: a, count: n})
		}
	}
	return out
}

// yamlQuote renders an arbitrary diagnostic line as a YAML double-quoted scalar. Diagnostic
// text is tool output — colons, quotes, backslashes and braces are all routine — so it is
// always quoted rather than heuristically left bare.
func yamlQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
