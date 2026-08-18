package agentteams

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Declarative apply-form renderers — the PII redaction.
// ---------------------------------------------------------------------------

func TestRenderWorkers_ExcludesPII(t *testing.T) {
	w := workerResp{
		Name:           "bed-worker",
		Phase:          "Running",
		ContainerState: "running",
		Model:          "claude-sonnet-5",
		Runtime:        "container",
		Image:          "ghcr.io/opencharly/agentteams-worker:latest",
		Identity:       "bed-worker",
		Skills:         []string{"forge-spike"},
		MatrixUserID:   "@bed-worker:matrix-local.agentteams.io:8080",
		RoomID:         "!abc123:matrix-local.agentteams.io:8080",
		Message:        "spawning manager",
		Team:           "replicator",
		Role:           "worker",
	}
	res := renderWorkers([]workerResp{w})
	if len(res) != 1 {
		t.Fatalf("renderWorkers returned %d resources, want 1", len(res))
	}
	spec := res[0].Spec
	for _, want := range []string{"model", "runtime", "image", "identity", "skills"} {
		if _, ok := spec[want]; !ok {
			t.Errorf("spec missing declarative field %q", want)
		}
	}
	for _, forbidden := range []string{"phase", "containerState", "matrixUserID", "roomID", "message", "team", "role"} {
		if _, ok := spec[forbidden]; ok {
			t.Errorf("spec leaked runtime/PII field %q", forbidden)
		}
	}
}

func TestRenderTeams_WorkerMembers(t *testing.T) {
	tr := teamResp{
		Name:         "replicator",
		Phase:        "Running",
		Description:  "the snapshot/hydrate team",
		LeaderName:   "replicator-leader",
		LeaderReady:  true,
		ReadyWorkers: 1,
		TotalWorkers: 2,
		Message:      "all ready",
		WorkerNames:  []string{"replicator-leader", "replicator-worker"},
	}
	res := renderTeams([]teamResp{tr})
	if len(res) != 1 {
		t.Fatalf("renderTeams returned %d resources, want 1", len(res))
	}
	spec := res[0].Spec
	if spec["description"] != "the snapshot/hydrate team" {
		t.Errorf("description = %v, want the snapshot/hydrate team", spec["description"])
	}
	members, ok := spec["workerMembers"].([]map[string]any)
	if !ok {
		t.Fatalf("workerMembers = %T, want []map[string]any", spec["workerMembers"])
	}
	if len(members) != 2 {
		t.Fatalf("workerMembers has %d entries, want 2", len(members))
	}
	if members[0]["name"] != "replicator-leader" || members[0]["role"] != "team_leader" {
		t.Errorf("leader member = %v, want {replicator-leader team_leader}", members[0])
	}
	if members[1]["name"] != "replicator-worker" || members[1]["role"] != "worker" {
		t.Errorf("worker member = %v, want {replicator-worker worker}", members[1])
	}
	for _, forbidden := range []string{"phase", "leaderReady", "readyWorkers", "totalWorkers", "message"} {
		if _, ok := spec[forbidden]; ok {
			t.Errorf("spec leaked runtime field %q", forbidden)
		}
	}
}

func TestRenderHumans_ExcludesPII(t *testing.T) {
	h := humanResp{
		Name:              "alice",
		Phase:             "Active",
		DisplayName:       "Alice",
		PermissionLevel:   2,
		AccessibleTeams:   []string{"replicator"},
		AccessibleWorkers: []string{"bed-worker"},
		MatrixUserID:      "@alice:matrix-local.agentteams.io:8080",
		InitialPassword:   "s3cret",
		Rooms:             []string{"!abc:matrix-local.agentteams.io:8080"},
		Message:           "welcome",
	}
	res := renderHumans([]humanResp{h})
	if len(res) != 1 {
		t.Fatalf("renderHumans returned %d resources, want 1", len(res))
	}
	spec := res[0].Spec
	if spec["displayName"] != "Alice" || spec["permissionLevel"] != 2 {
		t.Errorf("spec = %v, want displayName Alice + permissionLevel 2", spec)
	}
	for _, forbidden := range []string{"phase", "matrixUserID", "initialPassword", "rooms", "message"} {
		if _, ok := spec[forbidden]; ok {
			t.Errorf("spec leaked runtime/PII field %q", forbidden)
		}
	}
}

// ---------------------------------------------------------------------------
// PII redaction.
// ---------------------------------------------------------------------------

func TestScrubPII(t *testing.T) {
	in := "user @alice:matrix-local.agentteams.io:8080 in room !abc123:matrix-local.agentteams.io:8080 " +
		"alias #general:matrix-local.agentteams.io:8080 via Tuwunel and tuwunel"
	got, n := scrubPII(in)
	if n != 5 {
		t.Fatalf("scrubPII count = %d, want 5", n)
	}
	if strings.Contains(got, "matrix-local.agentteams.io") {
		t.Errorf("scrubbed output still contains a Matrix reference: %q", got)
	}
	if strings.Contains(got, "tuwunel") {
		t.Errorf("scrubbed output still contains tuwunel: %q", got)
	}
	if strings.Count(got, "<redacted>") != 5 {
		t.Errorf("scrubbed output has %d <redacted> markers, want 5: %q", strings.Count(got, "<redacted>"), got)
	}
}

func TestScrubPII_NoMatch(t *testing.T) {
	got, n := scrubPII("plain prose, no identity references")
	if n != 0 || got != "plain prose, no identity references" {
		t.Fatalf("scrubPII = (%q, %d), want unchanged, 0", got, n)
	}
}

func TestCountPIIRefs(t *testing.T) {
	dir := t.TempDir()
	// A clean file.
	if err := os.WriteFile(filepath.Join(dir, "workers.yml"), []byte("model: claude-sonnet-5\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if n, err := countPIIRefs(dir); err != nil || n != 0 {
		t.Fatalf("countPIIRefs(clean) = (%d, %v), want (0, nil)", n, err)
	}
	// A file with a surviving Matrix ref.
	if err := os.WriteFile(filepath.Join(dir, "teams.yml"), []byte("description: room !abc:matrix-local.agentteams.io:8080\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if n, err := countPIIRefs(dir); err != nil || n != 1 {
		t.Fatalf("countPIIRefs(dirty) = (%d, %v), want (1, nil)", n, err)
	}
}

// ---------------------------------------------------------------------------
// MinIO object mirroring.
// ---------------------------------------------------------------------------

func TestSkipObject(t *testing.T) {
	for _, excluded := range []string{".openclaw/matrix/crypto.db", ".openclaw/canvas/state.json", "credentials/matrix.json"} {
		if !skipObject(excluded) {
			t.Errorf("skipObject(%q) = false, want true", excluded)
		}
	}
	for _, kept := range []string{"agents/bed-worker/state.json", "shared/context.md"} {
		if skipObject(kept) {
			t.Errorf("skipObject(%q) = true, want false", kept)
		}
	}
}

func TestS3EscapePath(t *testing.T) {
	got := s3EscapePath("agents/bed worker/state.json")
	if got != "agents/bed%20worker/state.json" {
		t.Errorf("s3EscapePath = %q, want agents/bed%%20worker/state.json", got)
	}
}

// ---------------------------------------------------------------------------
// Bundle writing — the scrub is applied to the emitted YAML.
// ---------------------------------------------------------------------------

func TestWriteBundleYAML_Scrubs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "workers.yml")
	redacted := 0
	res := []yamlResource{{
		APIVersion: "agentteams.io/v1beta1",
		Kind:       "Worker",
		Metadata:   yamlMetadata{Name: "bed-worker"},
		Spec: map[string]any{
			"model": "claude-sonnet-5",
			// A prose field carrying a residual Matrix reference — the scrub must
			// catch it even though the renderer never emits the identity fields.
			"identity": "@bed-worker:matrix-local.agentteams.io:8080",
		},
	}}
	if err := writeBundleYAML(path, res, &redacted); err != nil {
		t.Fatal(err)
	}
	if redacted != 1 {
		t.Errorf("redacted count = %d, want 1", redacted)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "matrix-local.agentteams.io") {
		t.Errorf("written bundle still contains a Matrix reference: %q", string(data))
	}
	if !strings.Contains(string(data), "<redacted>") {
		t.Errorf("written bundle lacks the <redacted> marker: %q", string(data))
	}
}
