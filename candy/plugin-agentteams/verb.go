package agentteams

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/opencharly/charly/candy/plugin-agentteams/params"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/spec/spec"
)

// verb.go is the `agentteams:` check VERB — the declarative controller-probe
// counterpart of the `charly agentteams` command plugin. It is HOST-BASED (the
// mcp pattern): the provider resolves the controller's in-venue :8090 to a
// host-routable address via the reverse channel (a published port on the pod
// substrate, a live ssh -L forward on the vm substrate — spec/checkhost
// EndpointForVenue), pulls the admin SA token from the venue over the executor,
// and probes the controller with the SAME apiClient the command plugin uses
// (R3 — one REST surface covers the CLI and every bed). The verb's method +
// method-exclusive modifiers ride the desugared plugin input (params.AgentTeamsInput —
// the generated #AgentTeamsInput from schema/agentteams.cue), validated at
// runtime against the served schema.

// managerResp / managerListResp — the controller's manager shapes (upstream
// agentscope-ai/AgentTeams resource_handler.go: the list endpoint returns an
// object {"managers": [...], "total": N}, not a bare array).
type managerResp struct {
	Name         string `json:"name"`
	Phase        string `json:"phase"`
	State        string `json:"state"`
	Model        string `json:"model"`
	Runtime      string `json:"runtime"`
	Image        string `json:"image"`
	MatrixUserID string `json:"matrixUserID"`
	RoomID       string `json:"roomID"`
	Version      string `json:"version"`
	Message      string `json:"message"`
	WelcomeSent  bool   `json:"welcomeSent"`
}

type managerListResp struct {
	Managers []managerResp `json:"managers"`
	Total    int           `json:"total"`
}

// verbAgentTeamsClient is the verb's probe client: the SAME apiClient the
// command plugin uses, constructed against the host-routable controller address
// resolved by the reverse channel (R3 — one REST surface covers every bed).
type verbAgentTeamsClient struct {
	apiClient
}

func newVerbClient(addr, token string) *verbAgentTeamsClient {
	return &verbAgentTeamsClient{apiClient{
		baseURL: "http://" + addr,
		token:   token,
		http:    &http.Client{Timeout: 30 * time.Second},
	}}
}

// runVerbAgentTeams is the verb dispatch: resolve the controller address over
// the reverse channel, pull the admin SA token from the venue, probe with the
// shared apiClient, and grade with sdk.VerbVerdict.
func runVerbAgentTeams(ctx context.Context, cc kit.CheckContext, op *spec.Op, in params.AgentTeamsInput) (string, error) {
	// Resolve the controller's in-venue :8090 to a host-routable address: a
	// published port on the pod substrate, a live ssh -L forward on the vm
	// substrate (spec/checkhost EndpointForVenue).
	addr, err := cc.ResolveEndpoint(ctx, 8090)
	if err != nil {
		return "", fmt.Errorf("resolve controller endpoint: %w", err)
	}
	if addr == "" {
		return "", fmt.Errorf("no live venue for the agentteams verb (box-mode or no controller)")
	}
	// Pull the admin SA token from the venue over the executor.
	stdout, _, _, err := cc.Exec().RunCapture(ctx, "cat /var/run/agentteams/cli-token")
	if err != nil {
		return "", fmt.Errorf("read controller token: %w", err)
	}
	token := strings.TrimSpace(stdout)
	if token == "" {
		return "", fmt.Errorf("controller token is empty")
	}
	client := newVerbClient(addr, token)
	switch in.Method {
	case "status":
		return verbStatus(client)
	case "manager-running":
		return verbManagerRunning(ctx, client, in.Name, parseTimeout(op.Timeout, 300*time.Second))
	case "worker-running":
		return verbWorkerRunning(ctx, cc, client, in.Name, parseTimeout(op.Timeout, 300*time.Second))
	case "worker-list":
		return verbWorkerList(client)
	case "snapshot":
		return verbSnapshot(ctx, cc, client, in.Out, in.NoMinio)
	case "hydrate":
		return verbHydrate(ctx, cc, client, in.Bundle, in.NoMinio)
	default:
		return "", fmt.Errorf("unknown agentteams method %q", in.Method)
	}
}

// verbSnapshot runs the snapshot core host-side: the bundle is written to a HOST
// path (the verb is host-based — the mcp pattern), the MinIO credentials are
// pulled from the venue over the executor (the controller container carries
// AGENTTEAMS_MINIO_USER/PASSWORD in its env), and the SAME runSnapshot core the
// in-venue command uses does the work (R3).
func verbSnapshot(ctx context.Context, cc kit.CheckContext, client *verbAgentTeamsClient, out string, noMinio bool) (string, error) {
	if out == "" {
		out = "/tmp/agentteams-snapshot"
	}
	var s3 *s3Client
	if !noMinio {
		s3c, err := verbS3Client(ctx, cc)
		if err != nil {
			return "", err
		}
		s3 = s3c
	}
	return runSnapshot(ctx, &client.apiClient, s3, out)
}

// verbHydrate runs the hydrate core host-side: the bundle is read from a HOST
// path, applied back to the controller (workers → teams → humans), and the
// mirrored MinIO objects are restored.
func verbHydrate(ctx context.Context, cc kit.CheckContext, client *verbAgentTeamsClient, bundle string, noMinio bool) (string, error) {
	if bundle == "" {
		return "", fmt.Errorf("hydrate requires bundle: <dir>")
	}
	var s3 *s3Client
	if !noMinio {
		s3c, err := verbS3Client(ctx, cc)
		if err != nil {
			return "", err
		}
		s3 = s3c
	}
	return runHydrate(ctx, &client.apiClient, s3, bundle)
}

// verbS3Client builds the S3 client for the host-side verb: resolve the MinIO
// endpoint (:9000) over the reverse channel and pull the MinIO credentials from
// the venue over the executor (venueMinioCreds — the deploy-set env when a
// deploy overrides it, else the self-provisioned root-password file).
func verbS3Client(ctx context.Context, cc kit.CheckContext) (*s3Client, error) {
	addr, err := cc.ResolveEndpoint(ctx, 9000)
	if err != nil {
		return nil, fmt.Errorf("resolve minio endpoint: %w", err)
	}
	if addr == "" {
		return nil, fmt.Errorf("no live venue for the minio endpoint")
	}
	user, pass, err := venueMinioCreds(ctx, cc)
	if err != nil {
		return nil, err
	}
	bucket := "agentteams-storage"
	if v, _ := venueEnv(ctx, cc, "AGENTTEAMS_FS_BUCKET"); v != "" {
		bucket = v
	}
	prefix := "agentteams/agentteams-storage"
	if v, _ := venueEnv(ctx, cc, "AGENTTEAMS_STORAGE_PREFIX"); v != "" {
		prefix = v
	}
	return &s3Client{
		endpoint: "http://" + addr,
		bucket:   bucket,
		prefix:   prefix,
		user:     user,
		pass:     pass,
		http:     &http.Client{Timeout: 60 * time.Second},
	}, nil
}

// venueMinioCreds resolves the MinIO root credentials from the venue. The
// deploy-overridable AGENTTEAMS_MINIO_USER/PASSWORD are NOT on the container env
// (the controller's start script resolves them with :- defaults and exports them
// only into its own process — supervisord environment= would override the pod's
// `charly config -e` overrides), so a bare printenv finds nothing on a default
// deploy. The canonical source is the self-provisioned root-password file on the
// uid-1000 volume (~/.agentteams/minio/.root-password) — the SAME file the minio
// server and the controller's OSS client read. The user defaults to "admin".
func venueMinioCreds(ctx context.Context, cc kit.CheckContext) (user, pass string, err error) {
	user, _ = venueEnv(ctx, cc, "AGENTTEAMS_MINIO_USER")
	pass, _ = venueEnv(ctx, cc, "AGENTTEAMS_MINIO_PASSWORD")
	if user == "" {
		user = "admin"
	}
	if pass != "" {
		return user, pass, nil
	}
	// Self-provisioned fallback: read the root-password file from the uid-1000
	// volume. getent resolves the home deterministically regardless of the exec
	// user (root on a pod venue, the image user on a machine venue).
	stdout, _, _, err := cc.Exec().RunCapture(ctx,
		`cat "$(getent passwd 1000 | cut -d: -f6)/.agentteams/minio/.root-password"`)
	if err != nil {
		return "", "", fmt.Errorf("minio credentials not found in the venue (AGENTTEAMS_MINIO_PASSWORD unset and no self-provisioned root-password file)")
	}
	pass = strings.TrimSpace(stdout)
	if pass == "" {
		return "", "", fmt.Errorf("minio credentials not found in the venue (empty self-provisioned root-password)")
	}
	return user, pass, nil
}

// venueEnv reads one env var from the venue over the executor. An unset var
// (printenv exits non-zero) is not an error — the caller decides whether the
// value is required.
func venueEnv(ctx context.Context, cc kit.CheckContext, name string) (string, error) {
	stdout, _, _, err := cc.Exec().RunCapture(ctx, "printenv "+name)
	if err != nil {
		return "", nil
	}
	return strings.TrimSpace(stdout), nil
}

// verbStatus prints the controller's cluster status summary.
func verbStatus(client *verbAgentTeamsClient) (string, error) {
	var resp clusterStatusResp
	if err := client.do("GET", "/api/v1/status", nil, &resp); err != nil {
		return "", fmt.Errorf("get status: %w", err)
	}
	return fmt.Sprintf("Mode: %s\nTotal Workers: %d\nTotal Teams: %d\nTotal Humans: %d\n",
		resp.KubeMode, resp.TotalWorkers, resp.TotalTeams, resp.TotalHumans), nil
}

// verbManagerRunning polls the managers list until a manager reaches Running
// phase. With name: set it matches that specific manager; without it, any
// manager reaching Running satisfies the assertion (the default manager the
// controller spawns at startup).
func verbManagerRunning(ctx context.Context, client *verbAgentTeamsClient, name string, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	var last string
	for {
		var resp managerListResp
		if err := client.do("GET", "/api/v1/managers", nil, &resp); err != nil {
			last = err.Error()
		} else {
			for _, m := range resp.Managers {
				if name != "" && m.Name != name {
					continue
				}
				if m.Phase == "Running" {
					return fmt.Sprintf("manager %s Running (model=%s runtime=%s)\n", m.Name, m.Model, m.Runtime), nil
				}
				last = fmt.Sprintf("manager %s phase=%s", m.Name, or(m.Phase, "Pending"))
			}
		}
		if time.Now().After(deadline) {
			if last == "" {
				last = "no managers reported"
			}
			return "", fmt.Errorf("manager never reached Running: %s", last)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

// verbWorkerRunning creates the named Worker CR (idempotent — a 409 on a
// re-run is fine; the controller volume persists the CR and the poll reconciles
// it to Running), polls until it reaches Running WITH its Matrix room
// provisioned, then resolves the worker room alias via the public Matrix
// directory API on the homeserver's :6167 (the same shape the controller's own
// matrix client resolves).
func verbWorkerRunning(ctx context.Context, cc kit.CheckContext, client *verbAgentTeamsClient, name string, timeout time.Duration) (string, error) {
	if name == "" {
		name = "bed-worker"
	}
	// Create the Worker CR (containerManaged defaults to true; the runtime
	// resolves to openclaw and the image to AGENTTEAMS_WORKER_IMAGE). A 409
	// (already exists) is fine on a fresh `charly update` re-run.
	_ = client.do("POST", "/api/v1/workers", map[string]string{"name": name}, nil)

	deadline := time.Now().Add(timeout)
	var last string
	for {
		var resp workerResp
		if err := client.do("GET", "/api/v1/workers/"+name, nil, &resp); err != nil {
			last = err.Error()
		} else {
			if resp.Phase == "Running" && strings.HasPrefix(resp.RoomID, "!") {
				// The room exists on the homeserver: resolve the worker room
				// alias via the public Matrix directory API.
				alias := fmt.Sprintf("#agentteams-worker-%s:matrix-local.agentteams.io:8080", name)
				room, err := resolveMatrixRoom(ctx, cc, client, alias)
				if err != nil {
					return "", fmt.Errorf("worker %s Running but room alias unresolvable: %w", name, err)
				}
				return fmt.Sprintf("worker %s Running, room provisioned, room alias resolvable (%s)\n", name, room), nil
			}
			last = fmt.Sprintf("worker %s phase=%s roomID=%s", name, or(resp.Phase, "Pending"), resp.RoomID)
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("worker never reached Running with a room: %s", last)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

// resolveMatrixRoom resolves a Matrix room alias on the homeserver's public
// directory API (:6167 — the same port the bed's raw curl used). The alias is
// URL-encoded (# → %23, : → %3A) exactly as the controller's own client sends
// it. Reuses the apiClient's http client (R3).
func resolveMatrixRoom(ctx context.Context, cc kit.CheckContext, client *verbAgentTeamsClient, alias string) (string, error) {
	addr, err := cc.ResolveEndpoint(ctx, 6167)
	if err != nil {
		return "", fmt.Errorf("resolve matrix endpoint: %w", err)
	}
	if addr == "" {
		return "", fmt.Errorf("no live venue for the matrix directory API")
	}
	enc := url.QueryEscape(alias)
	req, err := http.NewRequestWithContext(ctx, "GET", "http://"+addr+"/_matrix/client/v3/directory/room/"+enc, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("matrix directory API status %d", resp.StatusCode)
	}
	var out struct {
		RoomID string `json:"room_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if !strings.HasPrefix(out.RoomID, "!") {
		return "", fmt.Errorf("room alias resolved to no room: %q", out.RoomID)
	}
	return out.RoomID, nil
}

// verbWorkerList prints the workers the controller manages.
func verbWorkerList(client *verbAgentTeamsClient) (string, error) {
	var resp workerListResp
	if err := client.do("GET", "/api/v1/workers", nil, &resp); err != nil {
		return "", fmt.Errorf("list workers: %w", err)
	}
	if resp.Total == 0 {
		return "No workers found.\n", nil
	}
	var b strings.Builder
	for _, w := range resp.Workers {
		fmt.Fprintf(&b, "%s\t%s\t%s\t%s\n", w.Name, or(w.Phase, "Pending"), w.Model, or(w.Runtime, "openclaw"))
	}
	return b.String(), nil
}

// parseTimeout interprets the shared #Op timeout (a duration string), falling
// back to def when empty or unparseable — the same helper shape plugin-kube's
// cluster.go uses.
func parseTimeout(s string, def time.Duration) time.Duration {
	if s == "" {
		return def
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return def
	}
	return d
}
