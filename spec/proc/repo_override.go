package proc

// repo_override.go — RDD local-override plumbing (#55 W3 B2-full, promoted from
// charly/refs.go). RepoOverrideEnv / SelfSuperprojectOverridePair / MergeRepoOverrides were
// core-private, but the "stays core" framing was a domain claim, not a process-boundary one: both
// functions are pure `git`-shelling + string manipulation over spec.RootRepoIdentity, with zero
// registry coupling — freely callable from any process on the host. Promoted here (spec/proc
// already hosts RunCharlySubcommand, the sibling process-fabric primitive) so BOTH charly core
// (charly/plugin_loader.go's deployNodePluginContext, itself relocated from check_cmd.go at #55 W3
// B3) and a compiled-in plugin (candy/plugin-check's bed session, which computes its OWN
// repo-override before self-loading the project) share ONE implementation instead of the plugin
// needing a second copy.

import (
	"os/exec"
	"strings"

	"github.com/opencharly/spec/spec"
)

// RepoOverrideEnv configures RDD local-overrides: it points a remote `@github`
// repo ref at a LOCAL working tree (Go-`replace`-style), so an UNCOMMITTED
// candy / charly.yml change can be built and `charly check`'d by ANY
// consumer — across submodule boundaries — BEFORE it is committed and pushed.
// This is the supported "verify before you push to main" mechanism (no cache
// hacks, no producer-first tag churn).
//
// Value: a comma-separated list of `repoPath=localDir` pairs. repoPath matches
// the repo-root form every `@github` candy/namespace/image ref resolves through
// (`github.com/<org>/<repo>`); a bare `<org>/<repo>` is accepted too (auto
// `github.com/` prefix, same rule as `--repo`). Example:
//
//	CHARLY_REPO_OVERRIDE=opencharly/charly=/home/me/oc-charly \
//	    charly -C box/ubuntu box build ubuntu-coder
//
// The matched directory resolves verbatim (leading `~/` expanded); the ref's
// `:vTAG` is IGNORED — an override ALWAYS resolves to the dev's current tree.
const RepoOverrideEnv = "CHARLY_REPO_OVERRIDE"

// SelfSuperprojectOverridePair returns a CHARLY_REPO_OVERRIDE pair
// (`<repo-identity>=<superproject-dir>`) that points a bed project's OWN
// superproject `@github` refs at the local working tree, or "" when projectDir
// is not a git submodule of a charly superproject. A check bed (a `disposable: true` fleet) living in
// a `box/<distro>` submodule references its parent repo's shared candies via
// `@github.com/<org>/<parent>/candy/<name>:<tag>`; without this override the bed
// would build the PINNED REMOTE candy and so test STALE code — the candy-ref
// analogue of why the bed runner builds the toolchain with `--dev-local-pkg`. The
// override IGNORES the ref's `:vTAG`, so the bed always tests the dev's current
// tree. Returns "" when projectDir is its own root (its candies already resolve
// from the local tree) or when git / the superproject identity is unavailable.
func SelfSuperprojectOverridePair(projectDir string) string {
	out, err := exec.Command("git", "-C", projectDir, "rev-parse", "--show-superproject-working-tree").Output()
	if err != nil {
		return ""
	}
	superDir := strings.TrimSpace(string(out))
	if superDir == "" {
		return "" // not a submodule — its candies already resolve from the local tree
	}
	identity := spec.RootRepoIdentity(superDir)
	if identity == "" {
		return ""
	}
	return identity + "=" + superDir
}

// MergeRepoOverrides combines an existing CHARLY_REPO_OVERRIDE value with an
// auto-added pair. The existing (operator-set) entries are placed FIRST so an
// explicit operator override for a repo WINS over the auto pair — repoOverrideDir
// returns the FIRST matching entry. Either argument may be empty.
func MergeRepoOverrides(existing, add string) string {
	existing = strings.TrimSpace(existing)
	add = strings.TrimSpace(add)
	switch {
	case existing == "":
		return add
	case add == "":
		return existing
	default:
		return existing + "," + add
	}
}
