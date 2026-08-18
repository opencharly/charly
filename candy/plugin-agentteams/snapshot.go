package agentteams

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// snapshot.go — the Replicator's snapshot→hydrate round-trip (Unit 3 of the
// factory north-star). snapshot GETs the workers/teams/humans via the shared
// apiClient, renders each to the declarative `apply -f` YAML form (the PII
// redaction: runtime/identity fields — matrixUserID, roomID, rooms,
// initialPassword, phase, message, … — are never emitted), scrubs any residual
// Matrix/Tuwunel reference from the prose fields, mirrors the referenced MinIO
// objects, and writes ONE hydration bundle dir. hydrate applies the bundle back
// (workers → teams → humans in dependency order) and restores the MinIO
// objects. The SAME cores serve the in-venue `charly agentteams snapshot`
// command AND the host-side `agentteams: {method: snapshot|hydrate}` verb
// methods (R3 — one surface, two placements).

// AgentTeamsSnapshotCmd is the `charly agentteams snapshot` command — the
// Replicator's in-venue tool. It runs against the controller the apiClient
// resolves (AGENTTEAMS_CONTROLLER_URL, default http://127.0.0.1:8090) with the
// token discoverToken() finds (env → token file → the controller-projected SA
// token at /var/run/secrets/agentteams/token → the controller's minted admin
// token at /var/run/agentteams/cli-token).
type AgentTeamsSnapshotCmd struct {
	Out     string `name:"out" help:"Output directory for the hydration bundle (default: ./agentteams-snapshot)"`
	NoMinio bool   `name:"no-minio" help:"Skip mirroring the referenced MinIO objects"`
}

func (c *AgentTeamsSnapshotCmd) Run() error {
	out := c.Out
	if out == "" {
		out = "agentteams-snapshot"
	}
	client := newAPIClient()
	var s3 *s3Client
	if !c.NoMinio {
		s3 = newS3ClientFromEnv()
	}
	summary, err := runSnapshot(context.Background(), client, s3, out)
	if err != nil {
		return err
	}
	fmt.Print(summary)
	return nil
}

// runSnapshot is the snapshot core shared by the command and the verb. It
// returns the human-readable summary (the verb's stdout).
func runSnapshot(ctx context.Context, client *apiClient, s3 *s3Client, out string) (string, error) {
	var workers workerListResp
	if err := client.do("GET", "/api/v1/workers", nil, &workers); err != nil {
		return "", fmt.Errorf("list workers: %w", err)
	}
	var teams teamListResp
	if err := client.do("GET", "/api/v1/teams", nil, &teams); err != nil {
		return "", fmt.Errorf("list teams: %w", err)
	}
	var humans humanListResp
	if err := client.do("GET", "/api/v1/humans", nil, &humans); err != nil {
		return "", fmt.Errorf("list humans: %w", err)
	}

	if err := os.MkdirAll(out, 0o755); err != nil {
		return "", fmt.Errorf("create bundle dir: %w", err)
	}

	// Render the declarative apply-form YAML (the PII redaction) and scrub any
	// residual Matrix/Tuwunel reference from the prose fields.
	redacted := 0
	if err := writeBundleYAML(filepath.Join(out, "workers.yml"), renderWorkers(workers.Workers), &redacted); err != nil {
		return "", err
	}
	if err := writeBundleYAML(filepath.Join(out, "teams.yml"), renderTeams(teams.Teams), &redacted); err != nil {
		return "", err
	}
	if err := writeBundleYAML(filepath.Join(out, "humans.yml"), renderHumans(humans.Humans), &redacted); err != nil {
		return "", err
	}

	// Mirror the referenced MinIO objects (the worker's own sync exclusions).
	mirrored := 0
	if s3 != nil {
		var err error
		mirrored, err = mirrorObjects(ctx, s3, out)
		if err != nil {
			return "", err
		}
	}

	// Defense in depth: the bundle must contain NO Matrix/Tuwunel PII reference —
	// a surviving one is a hard failure, never a warning (the bed asserting the
	// snapshot step passing IS the PII assertion).
	if n, err := countPIIRefs(out); err != nil {
		return "", err
	} else if n > 0 {
		return "", fmt.Errorf("snapshot bundle still contains %d Matrix/Tuwunel PII reference(s) — redaction incomplete", n)
	}

	manifest := map[string]any{
		"apiVersion": "agentteams.io/v1beta1",
		"kind":       "Snapshot",
		"metadata":   map[string]any{"name": "agentteams-snapshot"},
		"spec": map[string]any{
			"createdAt":    time.Now().UTC().Format(time.RFC3339),
			"workers":      len(workers.Workers),
			"teams":        len(teams.Teams),
			"humans":       len(humans.Humans),
			"minioObjects": mirrored,
			"piiRedacted":  redacted,
		},
	}
	if err := writeYAMLFile(filepath.Join(out, "snapshot.yml"), manifest); err != nil {
		return "", err
	}

	return fmt.Sprintf("Snapshot written to %s\n  workers: %d\n  teams: %d\n  humans: %d\n  minio objects mirrored: %d\n  PII refs redacted: %d\n",
		out, len(workers.Workers), len(teams.Teams), len(humans.Humans), mirrored, redacted), nil
}

// runHydrate is the hydrate core shared by the command and the verb: apply the
// bundle back (workers → teams → humans in dependency order — teams reference
// workers as members) and restore the mirrored MinIO objects.
func runHydrate(ctx context.Context, client *apiClient, s3 *s3Client, bundle string) (string, error) {
	lines, err := applyPathWithClient(client, bundle)
	if err != nil {
		return "", err
	}
	restored := 0
	if s3 != nil {
		minioDir := filepath.Join(bundle, "minio")
		if _, err := os.Stat(minioDir); err == nil {
			restored, err = restoreObjects(ctx, s3, minioDir)
			if err != nil {
				return "", err
			}
		}
	}
	return fmt.Sprintf("Hydrated %s\n%s  minio objects restored: %d\n", bundle, lines, restored), nil
}

// ---------------------------------------------------------------------------
// Declarative apply-form renderers — the PII redaction.
// ---------------------------------------------------------------------------

// renderWorkers renders each worker to the declarative apply form. Only the
// declarative spec fields the controller accepts on create/update are emitted;
// the runtime/identity fields (phase, containerState, matrixUserID, roomID,
// message, team, role) are never written — that IS the PII redaction.
func renderWorkers(workers []workerResp) []yamlResource {
	out := make([]yamlResource, 0, len(workers))
	for _, w := range workers {
		spec := map[string]any{}
		setIfNotEmpty(spec, "model", w.Model)
		setIfNotEmpty(spec, "runtime", w.Runtime)
		setIfNotEmpty(spec, "image", w.Image)
		setIfNotEmpty(spec, "identity", w.Identity)
		if len(w.Skills) > 0 {
			spec["skills"] = w.Skills
		}
		out = append(out, yamlResource{
			APIVersion: "agentteams.io/v1beta1",
			Kind:       "Worker",
			Metadata:   yamlMetadata{Name: w.Name},
			Spec:       spec,
		})
	}
	return out
}

// renderTeams renders each team to the declarative apply form. The leader and
// the worker names become the team's workerMembers (the apply surface's shape);
// the runtime fields (phase, leaderReady, readyWorkers, totalWorkers, message)
// are never written.
func renderTeams(teams []teamResp) []yamlResource {
	out := make([]yamlResource, 0, len(teams))
	for _, t := range teams {
		spec := map[string]any{}
		setIfNotEmpty(spec, "description", t.Description)
		members := make([]map[string]any, 0, len(t.WorkerNames)+1)
		if t.LeaderName != "" {
			members = append(members, map[string]any{"name": t.LeaderName, "role": "team_leader"})
		}
		for _, w := range t.WorkerNames {
			if w == t.LeaderName {
				continue
			}
			members = append(members, map[string]any{"name": w, "role": "worker"})
		}
		if len(members) > 0 {
			spec["workerMembers"] = members
		}
		out = append(out, yamlResource{
			APIVersion: "agentteams.io/v1beta1",
			Kind:       "Team",
			Metadata:   yamlMetadata{Name: t.Name},
			Spec:       spec,
		})
	}
	return out
}

// renderHumans renders each human to the declarative apply form. The runtime/
// identity fields (phase, matrixUserID, initialPassword, rooms, message) are
// never written.
func renderHumans(humans []humanResp) []yamlResource {
	out := make([]yamlResource, 0, len(humans))
	for _, h := range humans {
		spec := map[string]any{}
		setIfNotEmpty(spec, "displayName", h.DisplayName)
		spec["permissionLevel"] = h.PermissionLevel
		if len(h.AccessibleTeams) > 0 {
			spec["accessibleTeams"] = h.AccessibleTeams
		}
		if len(h.AccessibleWorkers) > 0 {
			spec["accessibleWorkers"] = h.AccessibleWorkers
		}
		out = append(out, yamlResource{
			APIVersion: "agentteams.io/v1beta1",
			Kind:       "Human",
			Metadata:   yamlMetadata{Name: h.Name},
			Spec:       spec,
		})
	}
	return out
}

// writeBundleYAML marshals the resources as a multi-doc YAML file, scrubbing
// each doc's prose for residual Matrix/Tuwunel PII references.
func writeBundleYAML(path string, resources []yamlResource, redacted *int) error {
	var b strings.Builder
	for i, res := range resources {
		if i > 0 {
			b.WriteString("---\n")
		}
		data, err := yaml.Marshal(res)
		if err != nil {
			return fmt.Errorf("marshal %s: %w", path, err)
		}
		scrubbed, n := scrubPII(string(data))
		*redacted += n
		b.WriteString(scrubbed)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func writeYAMLFile(path string, v any) error {
	data, err := yaml.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", path, err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// PII redaction — Matrix/Tuwunel references.
// ---------------------------------------------------------------------------

// piiPatterns match the identity references the snapshot must never carry: the
// Matrix user IDs / room IDs / room aliases on the deployment's homeserver
// domain, and the Tuwunel homeserver binary name itself.
var piiPatterns = []*regexp.Regexp{
	regexp.MustCompile(`@[a-zA-Z0-9._=-]+:matrix-local\.agentteams\.io:8080`),
	regexp.MustCompile(`![a-zA-Z0-9]+:matrix-local\.agentteams\.io:8080`),
	regexp.MustCompile(`#[a-zA-Z0-9._=-]+:matrix-local\.agentteams\.io:8080`),
	regexp.MustCompile(`(?i)tuwunel`),
}

// scrubPII replaces every Matrix/Tuwunel PII reference with <redacted> and
// returns the replacement count.
func scrubPII(s string) (string, int) {
	n := 0
	for _, re := range piiPatterns {
		s = re.ReplaceAllStringFunc(s, func(m string) string {
			n++
			return "<redacted>"
		})
	}
	return s, n
}

// countPIIRefs scans the bundle's YAML files for any surviving PII reference.
func countPIIRefs(dir string) (int, error) {
	n := 0
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yml") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return 0, err
		}
		for _, re := range piiPatterns {
			n += len(re.FindAllString(string(data), -1))
		}
	}
	return n, nil
}

// ---------------------------------------------------------------------------
// MinIO object mirroring.
// ---------------------------------------------------------------------------

// mirrorObjects copies the storage objects under the S3 prefix into the
// bundle's minio/ dir, applying the worker's own sync exclusions (the Matrix
// crypto state, the canvas, and the credentials — the credentials contain the
// Matrix password, which is PII).
func mirrorObjects(ctx context.Context, s3 *s3Client, out string) (int, error) {
	keys, err := s3.listObjects(ctx, s3.prefix)
	if err != nil {
		return 0, fmt.Errorf("list storage objects: %w", err)
	}
	mirrored := 0
	for _, key := range keys {
		rel := strings.TrimPrefix(key, s3.prefix+"/")
		if rel == key {
			continue // not under the storage prefix
		}
		if skipObject(rel) {
			continue
		}
		data, err := s3.getObject(ctx, key)
		if err != nil {
			return mirrored, fmt.Errorf("get object %s: %w", key, err)
		}
		dest := filepath.Join(out, "minio", rel)
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return mirrored, err
		}
		if err := os.WriteFile(dest, data, 0o644); err != nil {
			return mirrored, err
		}
		mirrored++
	}
	return mirrored, nil
}

// restoreObjects writes the bundle's minio/ objects back to the store.
func restoreObjects(ctx context.Context, s3 *s3Client, minioDir string) (int, error) {
	restored := 0
	err := filepath.Walk(minioDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(minioDir, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		key := s3.prefix + "/" + filepath.ToSlash(rel)
		if err := s3.putObject(ctx, key, data); err != nil {
			return err
		}
		restored++
		return nil
	})
	return restored, err
}

// skipObject reports whether a storage object is excluded from the snapshot —
// the same exclusions the worker's own `mc mirror` applies.
func skipObject(rel string) bool {
	for _, p := range []string{".openclaw/matrix/", ".openclaw/canvas/", "credentials/"} {
		if strings.HasPrefix(rel, p) {
			return true
		}
	}
	return false
}
