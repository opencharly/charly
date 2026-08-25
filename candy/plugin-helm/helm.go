package helm

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/opencharly/plugin-helm/candy/plugin-helm/params"
	"github.com/opencharly/sdk"
	"github.com/opencharly/spec/shellquote"
)

// helm.go is the venue-driving layer for BOTH helm capabilities: the step's
// `helm upgrade --install` invocation and the verb's four release-status assertion
// methods. Every operation shells out to the venue's `helm` binary over the host
// executor reverse channel (sdk.Executor.RunCapture) — the plugin owns NO Kubernetes
// client library. The kubeconfig resolution mirrors what a k3s guest needs: the
// operator's KUBECONFIG, else the k3s default /etc/rancher/k3s/k3s.yaml, else
// ~/.kube/config.

// kubeconfigPrelude exports the venue's kubeconfig for the helm invocation: the
// operator's KUBECONFIG wins, then the k3s guest default (/etc/rancher/k3s/k3s.yaml),
// then the standard ~/.kube/config. helm reads KUBECONFIG itself, so exporting it is
// the whole resolution.
const kubeconfigPrelude = `export KUBECONFIG="${KUBECONFIG:-}"; ` +
	`if [ -z "$KUBECONFIG" ] && [ -f /etc/rancher/k3s/k3s.yaml ]; then export KUBECONFIG=/etc/rancher/k3s/k3s.yaml; fi; ` +
	`if [ -z "$KUBECONFIG" ] && [ -f "$HOME/.kube/config" ]; then export KUBECONFIG="$HOME/.kube/config"; fi; `

// helmShellCmd wraps a helm command with the kubeconfig prelude.
func helmShellCmd(cmd string) string { return kubeconfigPrelude + cmd }

// helmCapture runs a helm command on the venue and returns its stdout, surfacing
// stderr on a non-zero exit.
func helmCapture(ctx context.Context, ex *sdk.Executor, cmd string) (string, error) {
	return ex.VenueCapture(ctx, helmShellCmd(cmd))
}

// ---------------------------------------------------------------------------
// step:helm-release — the `helm upgrade --install` invocation
// ---------------------------------------------------------------------------

// runHelmUpgradeInstall performs the step's IN-VENUE install: `helm upgrade --install
// <release> <chart>` with the optional repo/version/namespace/values/values_files/
// wait/timeout modifiers. The release is created if absent (upgrade --install) and
// upgraded in place on re-deploy — the idempotent install shape.
func runHelmUpgradeInstall(ctx context.Context, ex *sdk.Executor, in *params.HelmReleaseStep) error {
	args := []string{"helm", "upgrade", "--install", shellquote.ShellQuote(in.Release), shellquote.ShellQuote(in.Chart)}
	if in.Repo != "" {
		args = append(args, "--repo", shellquote.ShellQuote(in.Repo))
	}
	if in.Version != "" {
		args = append(args, "--version", shellquote.ShellQuote(in.Version))
	}
	if in.Namespace != "" {
		args = append(args, "--namespace", shellquote.ShellQuote(in.Namespace), "--create-namespace")
	}
	for _, f := range in.Values_files {
		args = append(args, "--values", shellquote.ShellQuote(f))
	}
	for _, set := range flattenValues(in.Values) {
		args = append(args, "--set", shellquote.ShellQuote(set))
	}
	if in.Wait {
		args = append(args, "--wait")
	}
	if in.Timeout != "" {
		args = append(args, "--timeout", shellquote.ShellQuote(in.Timeout))
	}
	_, stderr, exit, err := ex.RunCapture(ctx, helmShellCmd(strings.Join(args, " ")))
	if err != nil {
		return fmt.Errorf("helm upgrade --install: %w", err)
	}
	if exit != 0 {
		return fmt.Errorf("helm upgrade --install failed (exit %d): %s", exit, strings.TrimSpace(stderr))
	}
	return nil
}

// flattenValues renders a nested values map into `helm --set` arguments: leaf scalars
// become `key=value`, nested maps become dotted keys (`a.b=value`), and lists become
// indexed keys (`a[0]=value`). Deterministic (sorted keys) so the same input always
// produces the same --set set.
func flattenValues(values map[string]any) []string {
	var out []string
	var walk func(prefix string, v any)
	walk = func(prefix string, v any) {
		switch t := v.(type) {
		case map[string]any:
			keys := make([]string, 0, len(t))
			for k := range t {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				key := k
				if prefix != "" {
					key = prefix + "." + k
				}
				walk(key, t[k])
			}
		case []any:
			for i, item := range t {
				walk(fmt.Sprintf("%s[%d]", prefix, i), item)
			}
		default:
			out = append(out, fmt.Sprintf("%s=%s", prefix, scalarString(t)))
		}
	}
	walk("", values)
	return out
}

// scalarString renders a leaf value for `--set`: strings verbatim, numbers/bools via
// strconv, nil as the empty string.
func scalarString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", t)
	}
}

// ---------------------------------------------------------------------------
// verb:helm — the four release-status assertion methods
// ---------------------------------------------------------------------------

// dispatchVerb runs one helm assertion method against the venue and returns its
// captured output. A returned error is the verb FAILING; provider.go maps it through
// the exit_status / stderr matchers via sdk.VerbVerdict.
func dispatchVerb(ctx context.Context, ex *sdk.Executor, in *params.HelmInput) (string, error) {
	ns := in.Namespace
	if ns == "" {
		ns = "default"
	}
	switch in.Method {
	case "release-exists":
		return helmReleaseExists(ctx, ex, in.Release, ns)
	case "status":
		return helmStatus(ctx, ex, in.Release, ns, in.Status)
	case "revision":
		return helmRevision(ctx, ex, in.Release, ns, in.Revision)
	case "values-hash":
		return helmValuesHash(ctx, ex, in.Release, ns, in.Values_hash)
	}
	return "", fmt.Errorf("unknown helm method %q", in.Method)
}

// helmReleaseExists asserts the release is present in `helm list` for the namespace.
// Output is the matching `helm list` row (NAME NAMESPACE REVISION UPDATED STATUS
// CHART APP VERSION) so a `stdout: {contains: <release>}` matcher sees the release.
func helmReleaseExists(ctx context.Context, ex *sdk.Executor, release, ns string) (string, error) {
	out, err := helmCapture(ctx, ex, fmt.Sprintf("helm list -n %s -o json", shellquote.ShellQuote(ns)))
	if err != nil {
		return "", fmt.Errorf("helm list: %w", err)
	}
	var releases []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(out), &releases); err != nil {
		return "", fmt.Errorf("parsing helm list: %w", err)
	}
	for _, r := range releases {
		if r.Name == release {
			return fmt.Sprintf("release %s exists in namespace %s", release, ns), nil
		}
	}
	return "", fmt.Errorf("release %s not found in namespace %s", release, ns)
}

// helmStatus asserts the release's status (default "deployed") via `helm status -o
// json`. Output is the status line so a `stdout: {contains: deployed}` matcher works.
func helmStatus(ctx context.Context, ex *sdk.Executor, release, ns, want string) (string, error) {
	if want == "" {
		want = "deployed"
	}
	out, err := helmCapture(ctx, ex, fmt.Sprintf("helm status %s -n %s -o json", shellquote.ShellQuote(release), shellquote.ShellQuote(ns)))
	if err != nil {
		return "", fmt.Errorf("helm status: %w", err)
	}
	var st struct {
		Info struct {
			Status string `json:"status"`
		} `json:"info"`
	}
	if err := json.Unmarshal([]byte(out), &st); err != nil {
		return "", fmt.Errorf("parsing helm status: %w", err)
	}
	if st.Info.Status != want {
		return "", fmt.Errorf("release %s status is %q, want %q", release, st.Info.Status, want)
	}
	return fmt.Sprintf("release %s status: %s", release, st.Info.Status), nil
}

// helmRevision asserts the release's revision is ≥ the expected minimum via `helm
// status -o json`. Output is the revision line.
func helmRevision(ctx context.Context, ex *sdk.Executor, release, ns string, want int64) (string, error) {
	out, err := helmCapture(ctx, ex, fmt.Sprintf("helm status %s -n %s -o json", shellquote.ShellQuote(release), shellquote.ShellQuote(ns)))
	if err != nil {
		return "", fmt.Errorf("helm status: %w", err)
	}
	var st struct {
		Version int64 `json:"version"`
	}
	if err := json.Unmarshal([]byte(out), &st); err != nil {
		return "", fmt.Errorf("parsing helm status: %w", err)
	}
	if st.Version < want {
		return "", fmt.Errorf("release %s revision is %d, want ≥ %d", release, st.Version, want)
	}
	return fmt.Sprintf("release %s revision: %d (≥ %d)", release, st.Version, want), nil
}

// helmValuesHash asserts the SHA-256 of the release's rendered values (`helm get
// values -o json | sha256sum`) equals the expected hash. Output is the hash line.
func helmValuesHash(ctx context.Context, ex *sdk.Executor, release, ns, want string) (string, error) {
	out, err := helmCapture(ctx, ex, fmt.Sprintf("helm get values %s -n %s -o json | sha256sum", shellquote.ShellQuote(release), shellquote.ShellQuote(ns)))
	if err != nil {
		return "", fmt.Errorf("helm get values: %w", err)
	}
	got := strings.Fields(out)[0]
	if want != "" && got != want {
		return "", fmt.Errorf("release %s values hash is %s, want %s", release, got, want)
	}
	return fmt.Sprintf("release %s values sha256: %s", release, got), nil
}
