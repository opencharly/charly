package main

import (
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestNoOvTokensInSource asserts the ov→charly rebrand left NO `OV_` token in
// the runtime Go source outside the migration sentinel maps. This is the guard
// the per-emitter heredoc test lacked: it catches every mismatched heredoc
// CLOSER (the `\nOV_LEDGER_EOF` / `\nOV_WRITE` class — invisible to a `\bOV_`
// grep because the literal `\n` prefix removes the word boundary, which is why
// 8 of them survived the first sweep) AND any stray pre-rebrand env/sentinel
// token, in ONE assertion. migrate_*.go are excluded (their OV_*→CHARLY_* / CH_*
// transform maps legitimately name the legacy tokens).
func TestNoOvTokensInSource(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	// Catches OV_LEDGER_EOF (heredoc closers), OV=1 (probe markers), OV-PROPER
	// (hyphenated markers) and a standalone OV token — but NOT OVMF / OVERRIDE /
	// OVERLAY (OV followed by a letter has no `[_=-]` and isn't a whole word).
	ov := regexp.MustCompile(`\bOV[_=-]|\bOV\b`)
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || strings.HasPrefix(f, "migrate_") {
			continue
		}
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		if loc := ov.FindIndex(data); loc != nil {
			line := 1 + strings.Count(string(data[:loc[0]]), "\n")
			end := loc[0] + 16
			if end > len(data) {
				end = len(data)
			}
			t.Errorf("%s:%d: stray pre-rebrand OV_ token near %q — must be CHARLY_ (mismatched heredoc closer or sentinel)", f, line, string(data[loc[0]:end]))
		}
	}
}

// heredocOpener matches a shell here-document opener: `<<TAG` or `<<'TAG'`
// (the quote form suppresses expansion). Captures the delimiter TAG.
var heredocOpener = regexp.MustCompile(`<<'?([A-Za-z_][A-Za-z0-9_]*)'?`)

// assertBalancedHeredoc fails if any here-document opener in `out` lacks a
// matching standalone terminator line, OR if any heredoc delimiter still
// carries the pre-rebrand `OV_` prefix. This guards the exact regression the
// ov→charly rebrand introduced: openers were renamed `OV_X`→`CHARLY_X` but six
// closing terminators were left as `OV_X`, so the heredoc never terminated and
// the generated shell silently appended the spurious terminator line (and ran
// to EOF) — corrupting every file written via heredoc.
func assertBalancedHeredoc(t *testing.T, label, out string) {
	t.Helper()
	for _, m := range heredocOpener.FindAllStringSubmatch(out, -1) {
		tag := m[1]
		if strings.HasPrefix(tag, "OV_") {
			t.Errorf("%s: heredoc delimiter %q still uses the pre-rebrand OV_ prefix:\n%s", label, tag, out)
		}
		// The terminator must appear as its own line (a heredoc terminator is
		// matched only at the start of a line, optionally indented for <<-).
		terminated := false
		for line := range strings.SplitSeq(out, "\n") {
			if strings.TrimSpace(line) == tag {
				terminated = true
				break
			}
		}
		if !terminated {
			t.Errorf("%s: heredoc opened with <<%q has no matching terminator line (would consume to EOF):\n%s", label, tag, out)
		}
	}
}

// TestRenderTaskCommand_WriteHeredocBalanced covers the deploy-path `write:`
// task command (`install -m … <<'CHARLY_WRITE' … CHARLY_WRITE`).
func TestRenderTaskCommand_WriteHeredocBalanced(t *testing.T) {
	cmd, err := renderOpCommand(&OpStep{
		Op: &spec.Op{Write: "/etc/charly/demo.conf", Content: "key = value\n", Mode: "0644"},
	})
	if err != nil {
		t.Fatalf("renderTaskCommand: %v", err)
	}
	if !strings.Contains(cmd, "<<'CHARLY_WRITE'") {
		t.Errorf("expected CHARLY_WRITE opener, got: %s", cmd)
	}
	assertBalancedHeredoc(t, "renderTaskCommand(write)", cmd)
}

// TestOCIEmit_HeredocsBalanced covers the OCI build emitters: the structured
// repo-file write (`CHARLY_REPO`) and the packaged-service drop-in
// (`CHARLY_DROPIN`).
func TestOCIEmit_HeredocsBalanced(t *testing.T) {
	tgt := ociTestTarget(buildEngineContext{})
	plan := &InstallPlan{Candy: "demo", Steps: []InstallStep{
		&deploykit.RepoChangeStep{
			Format:  "rpm",
			File:    "/etc/yum.repos.d/demo.repo",
			Content: "[demo]\nname=demo\n",
		},
		&ServicePackagedStep{
			Unit:          "demo.service",
			TargetScope:   ScopeSystem,
			Enable:        true,
			OverridesText: "[Service]\nLimitNOFILE=65536\n",
			OverridesPath: "/etc/systemd/system/demo.service.d/override.conf",
			CandyName:     "demo",
		},
	}}
	if err := tgt.Emit([]*InstallPlan{plan}, EmitOpts{}); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	out := tgt.String()
	if !strings.Contains(out, "<<'CHARLY_REPO'") {
		t.Errorf("expected CHARLY_REPO opener in OCI output:\n%s", out)
	}
	if !strings.Contains(out, "<<'CHARLY_DROPIN'") {
		t.Errorf("expected CHARLY_DROPIN opener in OCI output:\n%s", out)
	}
	assertBalancedHeredoc(t, "deploykit.OCITarget.Emit", out)
}
