package check

// clone.go — per-run clone lifecycle + iteration commit + push-back (P12: relocated
// verbatim from charly/check_clone.go — pure git + file I/O, no host coupling).
//
// The harness's per-target driver clones the project's bind-mounted /workspace (or
// $PWD on host targets) into a per-run scratch dir on a fresh charlycheck/<run-id>
// branch. Per-iteration commits land in that clone. At end of run the branch pushes
// back to the project repo for an audit trail.
//
// All operations shell out to git; no external git library.

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// RunLayout is the canonical set of paths for one harness run.
//
// All harness state lives under <ProjectDir>/.check/<score>/. The directory is
// broadly gitignored but stays IN the project tree so Syncthing-style replication
// carries result files + NOTES.md memory across machines. Result files are durable
// per-run records; runs/ are bulky transient build artifacts the user can prune by
// hand.
//
// Same RunLayout shape applies to host / pod / vm targets — the only difference is
// which executor walks the tree. For pod targets the in-pod run-local writes under
// /workspace/.check/ (the bind-mounted project), so the host's mirrored copy is
// automatic.
type RunLayout struct {
	ProjectDir  string // project tree (workspace)
	Score       string // score name
	RunID       string // "<UTC-timestamp>-<shorthash>"
	HarnessRoot string // <ProjectDir>/.check/<score>
	RunDir      string // <HarnessRoot>/runs/<run-id>
	RepoDir     string // <HarnessRoot>/runs/<run-id>/repo (per-run clone)
	Branch      string // "charlycheck/<run-id>"
	// Phase, when > 0, segregates iteration dirs under
	// <RunDir>/phase<Phase>/iter<k>/. Set by the progressive caller before each
	// phase-RunHarness call. Zero = single-phase (legacy/non-progressive) runs
	// that write iter dirs directly under RunDir/iter<k>/.
	Phase int
}

// NewRunLayout constructs a RunLayout. Generates run-id if empty.
func NewRunLayout(projectDir, score, runID string) RunLayout {
	if runID == "" {
		runID = GenerateRunID()
	}
	if score == "" {
		score = "default"
	}
	root := HarnessDataRoot(projectDir, score)
	runDir := filepath.Join(root, "runs", runID)
	return RunLayout{
		ProjectDir:  projectDir,
		Score:       score,
		RunID:       runID,
		HarnessRoot: root,
		RunDir:      runDir,
		RepoDir:     filepath.Join(runDir, "repo"),
		Branch:      "charlycheck/" + runID,
	}
}

// HarnessDataRoot returns the absolute path to the harness data directory for this
// (project, score) pair: <projectDir>/.check/<score>. In-project location is
// deliberate — Syncthing-style file syncs replicate the durable result files +
// NOTES.md memory across the user's machines. .check/ is broadly gitignored.
func HarnessDataRoot(projectDir, score string) string {
	return filepath.Join(projectDir, ".check", score)
}

// GenerateRunID returns a fresh UTC-timestamp-prefixed identifier.
func GenerateRunID() string {
	ts := time.Now().UTC().Format("20060102-150405")
	buf := make([]byte, 3)
	if _, err := rand.Read(buf); err != nil {
		return ts + "-000000"
	}
	return ts + "-" + hex.EncodeToString(buf)
}

// IterDir returns the path for iteration k under this run. When the layout's Phase
// is > 0 (progressive scoring), paths are segregated under phase<Phase>/iter<k> so
// each phase has its own iter1/, iter2/, etc. without colliding across phase
// boundaries.
func (l RunLayout) IterDir(k int) string {
	if l.Phase > 0 {
		return filepath.Join(l.RunDir, fmt.Sprintf("phase%d", l.Phase), fmt.Sprintf("iter%d", k))
	}
	return filepath.Join(l.RunDir, fmt.Sprintf("iter%d", k))
}

// ResultsDir returns the per-entity results directory (sibling of runs/) under the
// harness data root.
func (l RunLayout) ResultsDir() string {
	return filepath.Join(l.HarnessRoot, "results")
}

// NoteDir returns the per-entity note directory under the harness data root.
func (l RunLayout) NoteDir() string {
	return filepath.Join(l.HarnessRoot, "note")
}

// CreateRunClone creates a per-run scratch clone at l.RepoDir on a fresh branch
// charlycheck/<run-id>. Source: l.ProjectDir.
func CreateRunClone(ctx context.Context, l RunLayout) error {
	if err := os.MkdirAll(l.RunDir, 0o755); err != nil {
		return fmt.Errorf("create run dir %s: %w", l.RunDir, err)
	}
	if _, err := os.Stat(l.RepoDir); err == nil {
		return fmt.Errorf("repo path %s already exists", l.RepoDir)
	}

	cloneCmd := exec.CommandContext(ctx, "git", "clone", "--no-local", l.ProjectDir, l.RepoDir)
	if out, err := cloneCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git clone --no-local %s %s: %w\n%s", l.ProjectDir, l.RepoDir, err, string(out))
	}

	branchCmd := exec.CommandContext(ctx, "git", "-C", l.RepoDir, "checkout", "-b", l.Branch)
	if out, err := branchCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git checkout -b %s: %w\n%s", l.Branch, err, string(out))
	}

	// Source submodule clones from the host working tree so locally-pinned commits
	// that haven't been pushed to origin still resolve. The override is benign when
	// origin DOES have the commit — git just fetches from the local path.
	if err := overrideSubmoduleUrlsToLocal(ctx, l.RepoDir, l.ProjectDir); err != nil {
		return fmt.Errorf("override submodule URLs: %w", err)
	}

	// `-c protocol.file.allow=always` lifts git's CVE-2022-39253 hardening for
	// local-path submodule URLs — required because the override above points each
	// submodule at the host's working-tree .git directory.
	subCmd := exec.CommandContext(ctx, "git",
		"-c", "protocol.file.allow=always",
		"-C", l.RepoDir, "submodule", "update", "--init", "--recursive")
	if out, err := subCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git submodule update --init --recursive in %s: %w\n%s", l.RepoDir, err, string(out))
	}

	for _, kv := range [][2]string{
		{"user.email", "check@opencharly.local"},
		{"user.name", "charly check"},
	} {
		c := exec.CommandContext(ctx, "git", "-C", l.RepoDir, "config", kv[0], kv[1])
		if out, err := c.CombinedOutput(); err != nil {
			return fmt.Errorf("git config %s in %s: %w\n%s", kv[0], l.RepoDir, err, string(out))
		}
	}

	// Mirror untracked working-tree artifacts the build needs.
	for _, sub := range []string{"bin", "candy/charly/bin"} {
		src := filepath.Join(l.ProjectDir, sub)
		st, err := os.Stat(src)
		if err != nil || !st.IsDir() {
			continue
		}
		dst := filepath.Join(l.RepoDir, sub)
		if err := os.MkdirAll(dst, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", dst, err)
		}
		cp := exec.CommandContext(ctx, "cp", "-a", src+"/.", dst+"/")
		if out, err := cp.CombinedOutput(); err != nil {
			return fmt.Errorf("cp -a %s -> %s: %w\n%s", src, dst, err, string(out))
		}
	}

	return nil
}

// PushBranchToHost pushes the per-run branch back to ProjectDir's git.
func PushBranchToHost(ctx context.Context, l RunLayout) error {
	target := "file://" + l.ProjectDir
	cmd := exec.CommandContext(ctx, "git", "-C", l.RepoDir, "push", target, l.Branch)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git push %s %s: %w\n%s", target, l.Branch, err, string(out))
	}
	return nil
}

// CommitIterationInRepo stages all changes + commits with the standard message.
// Hooks run; --allow-empty is on so no-op iterations leave a marker.
func CommitIterationInRepo(ctx context.Context, l RunLayout, k int, score int, solvedIDs []string) (string, error) {
	addCmd := exec.CommandContext(ctx, "git", "-C", l.RepoDir, "add", "-A")
	if out, err := addCmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("git add -A (iter%d): %w\n%s", k, err, string(out))
	}
	msg := formatCommitMessage(k, score, solvedIDs)
	commitCmd := exec.CommandContext(ctx, "git", "-C", l.RepoDir, "commit", "--allow-empty", "-m", msg)
	out, err := commitCmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git commit (iter%d) rejected: %w\n%s", k, err, string(out))
	}
	sha, err := resolveHeadSHA(ctx, l.RepoDir)
	if err != nil {
		return "", fmt.Errorf("commit succeeded but could not read HEAD: %w", err)
	}
	return sha, nil
}

func formatCommitMessage(k int, score int, solvedIDs []string) string {
	idsTrunc := truncateIDs(solvedIDs, 6)
	return fmt.Sprintf("iter%d: score=%d, solved=[%s]", k, score, strings.Join(idsTrunc, ","))
}

func truncateIDs(ids []string, maxN int) []string {
	if len(ids) <= maxN {
		return ids
	}
	out := append([]string(nil), ids[:maxN]...)
	return append(out, fmt.Sprintf("...+%d", len(ids)-maxN))
}

func resolveHeadSHA(ctx context.Context, repoDir string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", repoDir, "rev-parse", "HEAD")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return strings.TrimSpace(stdout.String()), nil
}

// RunSummary describes one past harness run found under .check/<score>/runs.
type RunSummary struct {
	Score        string
	RunID        string
	RunDir       string
	Status       string    // "complete" (result.{calver}.yml present) | "incomplete"
	StartedUTC   time.Time // parsed from RunID
	HasRepo      bool
	BranchExists bool
}

// ListRuns walks <projectDir>/.check/*/runs/ across all scores and returns one
// RunSummary per run dir. Sorted newest first.
func ListRuns(ctx context.Context, projectDir string) ([]RunSummary, error) {
	base := filepath.Join(projectDir, ".check")
	scores, err := os.ReadDir(base)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", base, err)
	}
	var out []RunSummary
	for _, rEntry := range scores {
		if !rEntry.IsDir() {
			continue
		}
		runsDir := filepath.Join(base, rEntry.Name(), "runs")
		entries, err := os.ReadDir(runsDir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			runID := e.Name()
			s := RunSummary{
				Score:  rEntry.Name(),
				RunID:  runID,
				RunDir: filepath.Join(runsDir, runID),
			}
			s.Status = "incomplete"
			resultsDir := filepath.Join(base, rEntry.Name(), "results")
			if results, err := os.ReadDir(resultsDir); err == nil {
				for _, r := range results {
					if strings.HasPrefix(r.Name(), "result.") && strings.HasSuffix(r.Name(), ".yml") {
						s.Status = "complete"
						break
					}
				}
			}
			if st, err := os.Stat(filepath.Join(s.RunDir, "repo")); err == nil && st.IsDir() {
				s.HasRepo = true
			}
			s.BranchExists = branchExists(ctx, projectDir, "charlycheck/"+runID)
			s.StartedUTC = parseRunIDTimestamp(runID)
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedUTC.After(out[j].StartedUTC) })
	return out, nil
}

func branchExists(ctx context.Context, projectDir, branch string) bool {
	cmd := exec.CommandContext(ctx, "git", "-C", projectDir,
		"show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	return cmd.Run() == nil
}

func parseRunIDTimestamp(runID string) time.Time {
	if len(runID) < 15 {
		return time.Time{}
	}
	stamp := runID[:15]
	t, err := time.Parse("20060102-150405", stamp)
	if err != nil {
		return time.Time{}
	}
	return t
}

// overrideSubmoduleUrlsToLocal rewrites every submodule.<name>.url in the
// freshly-cloned repo's .gitmodules to the host's working-tree submodule, then runs
// `git submodule sync` so .git/config picks up the override. Submodules whose host
// directory does not exist are left untouched.
func overrideSubmoduleUrlsToLocal(ctx context.Context, repoDir, projectDir string) error {
	out, err := exec.CommandContext(ctx, "git", "-C", repoDir,
		"config", "-f", ".gitmodules",
		"--get-regexp", `^submodule\..*\.path$`).Output()
	if err != nil {
		return nil
	}
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, " ", 2)
		if len(parts) != 2 {
			continue
		}
		key := parts[0]
		subPath := parts[1]
		name := strings.TrimSuffix(strings.TrimPrefix(key, "submodule."), ".path")
		hostSub := filepath.Join(projectDir, subPath)
		if _, err := os.Stat(filepath.Join(hostSub, ".git")); err != nil {
			continue
		}
		// Plain absolute path (NOT file://) — file:// triggers `transport 'file'
		// not allowed` even with the protocol.file.allow flag because git applies
		// that policy by URL scheme, not by resolved path.
		if err := exec.CommandContext(ctx, "git", "-C", repoDir,
			"config", "-f", ".gitmodules",
			"submodule."+name+".url", hostSub).Run(); err != nil {
			return fmt.Errorf("git config -f .gitmodules submodule.%s.url: %w", name, err)
		}
	}
	return exec.CommandContext(ctx, "git", "-C", repoDir,
		"submodule", "sync", "--recursive").Run()
}
