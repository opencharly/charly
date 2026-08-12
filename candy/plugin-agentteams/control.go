package agentteams

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// AgentTeamsCmd is the `charly agentteams` command tree — the kong grammar the
// host prescans into the CLI (via the reflected CLIModel) and the plugin
// kong-parses its pass-through args into (sdk.RunInProcCLI). Every leaf is a
// plain net/http REST call against the AgentTeams controller — the same surface
// the upstream `agt` CLI uses, with no upstream binary and no SDK.
type AgentTeamsCmd struct {
	Status AgentTeamsStatusCmd `cmd:"" help:"Show controller health and resource counts"`
	Worker AgentTeamsWorkerCmd `cmd:"" help:"Manage Worker CRs"`
	Team   AgentTeamsTeamCmd   `cmd:"" help:"Manage Teams"`
	Human  AgentTeamsHumanCmd  `cmd:"" help:"Manage Humans"`
	Apply  AgentTeamsApplyCmd  `cmd:"" help:"Apply declarative YAML (create-or-update per kind)"`
	Config AgentTeamsConfigCmd `cmd:"" help:"Show the resolved controller endpoint and token source"`
}

// ---------------------------------------------------------------------------
// REST client — the AgentTeams runtime contract, plain net/http.
// ---------------------------------------------------------------------------

// apiClient is a thin HTTP wrapper for the agentteams-controller REST API.
type apiClient struct {
	baseURL string
	token   string
	http    *http.Client
}

func newAPIClient() *apiClient {
	baseURL := os.Getenv("AGENTTEAMS_CONTROLLER_URL")
	if baseURL == "" {
		baseURL = "http://127.0.0.1:18090"
	}
	baseURL = strings.TrimRight(baseURL, "/")
	return &apiClient{
		baseURL: baseURL,
		token:   discoverToken(),
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

// discoverToken returns a bearer token using the AgentTeams runtime contract:
//  1. AGENTTEAMS_AUTH_TOKEN env var
//  2. AGENTTEAMS_AUTH_TOKEN_FILE token file
//  3. empty string (unauthenticated, for controllers with auth disabled)
func discoverToken() string {
	if token := os.Getenv("AGENTTEAMS_AUTH_TOKEN"); token != "" {
		return token
	}
	if path := os.Getenv("AGENTTEAMS_AUTH_TOKEN_FILE"); path != "" {
		if data, err := os.ReadFile(path); err == nil {
			if t := strings.TrimSpace(string(data)); t != "" {
				return t
			}
		}
	}
	return ""
}

// apiError represents a non-2xx response from the controller.
type apiError struct {
	StatusCode int
	Message    string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("HTTP %d: %s", e.StatusCode, e.Message)
}

// do sends a JSON request, checks for 2xx, and decodes the response body into
// result. body may be nil for methods that have no request body; result may be
// nil if the caller does not need the response body (e.g. DELETE → 204).
func (c *apiClient) do(method, path string, body, result any) error {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, c.baseURL+path, bodyReader)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(respBody))
		var errResp struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(respBody, &errResp) == nil && errResp.Error != "" {
			msg = errResp.Error
		}
		return &apiError{StatusCode: resp.StatusCode, Message: msg}
	}
	if result != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, result); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

// doMultipart uploads a file via multipart/form-data (the ZIP package import).
// fieldName is the form field name for the file (e.g. "file"); extra string
// key-value pairs are sent as form fields.
func (c *apiClient) doMultipart(path, fieldName, fileName string, fileData []byte, fields map[string]string, result any) error {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	for k, v := range fields {
		if err := writer.WriteField(k, v); err != nil {
			return fmt.Errorf("write field %s: %w", k, err)
		}
	}
	part, err := writer.CreateFormFile(fieldName, fileName)
	if err != nil {
		return fmt.Errorf("create form file: %w", err)
	}
	if _, err := part.Write(fileData); err != nil {
		return fmt.Errorf("write file data: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("close multipart writer: %w", err)
	}

	req, err := http.NewRequest("POST", c.baseURL+path, &buf)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(respBody))
		var errResp struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(respBody, &errResp) == nil && errResp.Error != "" {
			msg = errResp.Error
		}
		return &apiError{StatusCode: resp.StatusCode, Message: msg}
	}
	if result != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, result); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

// resourceExists checks whether a resource exists by issuing a GET request.
// Returns true on 2xx, false on 404, and an error for other status codes.
func (c *apiClient) resourceExists(path string) (bool, error) {
	req, err := http.NewRequest("GET", c.baseURL+path, nil)
	if err != nil {
		return false, fmt.Errorf("create request: %w", err)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return false, err
	}
	_ = resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return true, nil
	}
	if resp.StatusCode == http.StatusNotFound {
		return false, nil
	}
	return false, &apiError{StatusCode: resp.StatusCode, Message: "unexpected status checking resource"}
}

// ---------------------------------------------------------------------------
// Response types (lightweight, no K8s dependency — the agt CLI's shapes).
// ---------------------------------------------------------------------------

type clusterStatusResp struct {
	KubeMode     string `json:"kubeMode"`
	TotalWorkers int    `json:"totalWorkers"`
	TotalTeams   int    `json:"totalTeams"`
	TotalHumans  int    `json:"totalHumans"`
}

type workerResp struct {
	Name             string   `json:"name"`
	Phase            string   `json:"phase"`
	ContainerManaged bool     `json:"containerManaged"`
	State            string   `json:"state,omitempty"`
	Model            string   `json:"model,omitempty"`
	Runtime          string   `json:"runtime,omitempty"`
	Image            string   `json:"image,omitempty"`
	Identity         string   `json:"identity,omitempty"`
	Skills           []string `json:"skills,omitempty"`
	ContainerState   string   `json:"containerState,omitempty"`
	MatrixUserID     string   `json:"matrixUserID,omitempty"`
	RoomID           string   `json:"roomID,omitempty"`
	Message          string   `json:"message,omitempty"`
	Team             string   `json:"team,omitempty"`
	Role             string   `json:"role,omitempty"`
}

type workerListResp struct {
	Workers []workerResp `json:"workers"`
	Total   int          `json:"total"`
}

type teamResp struct {
	Name         string   `json:"name"`
	Phase        string   `json:"phase"`
	Description  string   `json:"description,omitempty"`
	LeaderName   string   `json:"leaderName"`
	LeaderReady  bool     `json:"leaderReady"`
	ReadyWorkers int      `json:"readyWorkers"`
	TotalWorkers int      `json:"totalWorkers"`
	Message      string   `json:"message,omitempty"`
	WorkerNames  []string `json:"workerNames,omitempty"`
}

type teamListResp struct {
	Teams []teamResp `json:"teams"`
	Total int        `json:"total"`
}

type humanResp struct {
	Name              string   `json:"name"`
	Phase             string   `json:"phase"`
	DisplayName       string   `json:"displayName"`
	PermissionLevel   int      `json:"permissionLevel"`
	AccessibleTeams   []string `json:"accessibleTeams,omitempty"`
	AccessibleWorkers []string `json:"accessibleWorkers,omitempty"`
	MatrixUserID      string   `json:"matrixUserID,omitempty"`
	InitialPassword   string   `json:"initialPassword,omitempty"`
	Rooms             []string `json:"rooms,omitempty"`
	Message           string   `json:"message,omitempty"`
}

type humanListResp struct {
	Humans []humanResp `json:"humans"`
	Total  int         `json:"total"`
}

// ---------------------------------------------------------------------------
// status
// ---------------------------------------------------------------------------

type AgentTeamsStatusCmd struct {
	Output string `short:"o" name:"output" help:"Output format (json)"`
}

func (c *AgentTeamsStatusCmd) Run() error {
	client := newAPIClient()
	var resp clusterStatusResp
	if err := client.do("GET", "/api/v1/status", nil, &resp); err != nil {
		return fmt.Errorf("get status: %w", err)
	}
	if c.Output == "json" {
		return printJSON(resp)
	}
	fmt.Printf("Mode: %s\n", resp.KubeMode)
	fmt.Printf("Total Workers: %d\n", resp.TotalWorkers)
	fmt.Printf("Total Teams: %d\n", resp.TotalTeams)
	fmt.Printf("Total Humans: %d\n", resp.TotalHumans)
	return nil
}

// ---------------------------------------------------------------------------
// config
// ---------------------------------------------------------------------------

type AgentTeamsConfigCmd struct{}

func (c *AgentTeamsConfigCmd) Run() error {
	baseURL := os.Getenv("AGENTTEAMS_CONTROLLER_URL")
	if baseURL == "" {
		baseURL = "http://127.0.0.1:18090"
	}
	tokenSource := "none"
	switch {
	case os.Getenv("AGENTTEAMS_AUTH_TOKEN") != "":
		tokenSource = "AGENTTEAMS_AUTH_TOKEN env"
	case os.Getenv("AGENTTEAMS_AUTH_TOKEN_FILE") != "":
		tokenSource = "AGENTTEAMS_AUTH_TOKEN_FILE (" + os.Getenv("AGENTTEAMS_AUTH_TOKEN_FILE") + ")"
	}
	fmt.Printf("Controller URL: %s\n", strings.TrimRight(baseURL, "/"))
	fmt.Printf("Token source: %s\n", tokenSource)
	return nil
}

// ---------------------------------------------------------------------------
// worker
// ---------------------------------------------------------------------------

type AgentTeamsWorkerCmd struct {
	List   AgentTeamsWorkerListCmd   `cmd:"" help:"List Workers"`
	Get    AgentTeamsWorkerGetCmd    `cmd:"" help:"Show one Worker"`
	Create AgentTeamsWorkerCreateCmd `cmd:"" help:"Create a Worker"`
	Update AgentTeamsWorkerUpdateCmd `cmd:"" help:"Update a Worker"`
	Apply  AgentTeamsWorkerApplyCmd  `cmd:"" help:"Apply a Worker (create-or-update, incl. ZIP package import)"`
	Delete AgentTeamsWorkerDeleteCmd `cmd:"" help:"Delete a Worker"`
}

type AgentTeamsWorkerListCmd struct {
	Team   string `name:"team" help:"Filter by team name"`
	Output string `short:"o" name:"output" help:"Output format (json)"`
}

func (c *AgentTeamsWorkerListCmd) Run() error {
	client := newAPIClient()
	path := "/api/v1/workers"
	if c.Team != "" {
		path += "?team=" + url.QueryEscape(c.Team)
	}
	var resp workerListResp
	if err := client.do("GET", path, nil, &resp); err != nil {
		return fmt.Errorf("list workers: %w", err)
	}
	if c.Output == "json" {
		return printJSON(resp)
	}
	if resp.Total == 0 {
		fmt.Println("No workers found.")
		return nil
	}
	rows := make([][]string, 0, len(resp.Workers))
	for _, w := range resp.Workers {
		rows = append(rows, []string{
			w.Name,
			or(w.Phase, "Pending"),
			w.Model,
			or(w.Team, "-"),
			or(w.Runtime, "openclaw"),
		})
	}
	printTable([]string{"NAME", "PHASE", "MODEL", "TEAM", "RUNTIME"}, rows)
	return nil
}

type AgentTeamsWorkerGetCmd struct {
	Name   string `arg:"" help:"Worker name"`
	Output string `short:"o" name:"output" help:"Output format (json)"`
}

func (c *AgentTeamsWorkerGetCmd) Run() error {
	client := newAPIClient()
	var resp workerResp
	if err := client.do("GET", "/api/v1/workers/"+c.Name, nil, &resp); err != nil {
		return fmt.Errorf("get worker: %w", err)
	}
	if c.Output == "json" {
		return printJSON(resp)
	}
	printDetail([]keyValue{
		{"Name", resp.Name},
		{"Phase", or(resp.Phase, "Pending")},
		{"Model", resp.Model},
		{"Runtime", or(resp.Runtime, "openclaw")},
		{"ContainerState", resp.ContainerState},
		{"Image", resp.Image},
		{"Team", resp.Team},
		{"Role", resp.Role},
		{"MatrixUserID", resp.MatrixUserID},
		{"RoomID", resp.RoomID},
		{"Message", resp.Message},
	})
	return nil
}

type AgentTeamsWorkerCreateCmd struct {
	Name        string        `name:"name" help:"Worker name (required)"`
	Model       string        `name:"model" help:"LLM model ID (default: $AGENTTEAMS_DEFAULT_MODEL, else qwen3.6-plus)"`
	Runtime     string        `name:"runtime" help:"Agent runtime (openclaw|copaw|qwenpaw|hermes|openhuman)"`
	Image       string        `name:"image" help:"Container image override"`
	Identity    string        `name:"identity" help:"Worker identity description"`
	Soul        string        `name:"soul" help:"Worker SOUL.md content (inline)"`
	SoulFile    string        `name:"soul-file" help:"Path to SOUL.md file (overrides --soul)"`
	Skills      string        `name:"skills" help:"Comma-separated built-in skills"`
	Package     string        `name:"package" help:"Package URI (nacos://, http://, oss://) or shorthand"`
	Expose      string        `name:"expose" help:"Comma-separated ports to expose (e.g. 8080,3000)"`
	NoWait      bool          `name:"no-wait" help:"Return immediately after the controller accepts the create request, without polling for Ready"`
	WaitTimeout time.Duration `name:"wait-timeout" default:"3m" help:"Maximum time to wait for the Worker to report Ready"`
	Output      string        `short:"o" name:"output" help:"Output format (json)"`
}

func (c *AgentTeamsWorkerCreateCmd) Run() error {
	if c.Name == "" {
		return fmt.Errorf("--name is required")
	}
	if err := validateWorkerName(c.Name); err != nil {
		return err
	}
	model := c.Model
	if model == "" {
		model = defaultWorkerModel()
	}
	soul := c.Soul
	if c.SoulFile != "" {
		data, err := os.ReadFile(c.SoulFile)
		if err != nil {
			return fmt.Errorf("read --soul-file %q: %w", c.SoulFile, err)
		}
		soul = string(data)
	}
	packageURI := c.Package
	if packageURI != "" {
		var err error
		packageURI, err = expandPackageURI(packageURI)
		if err != nil {
			return err
		}
	}

	req := map[string]any{"name": c.Name, "model": model}
	setIfNotEmpty(req, "runtime", c.Runtime)
	setIfNotEmpty(req, "image", c.Image)
	setIfNotEmpty(req, "identity", c.Identity)
	setIfNotEmpty(req, "soul", soul)
	setIfNotEmpty(req, "package", packageURI)
	if c.Skills != "" {
		req["skills"] = splitCSV(c.Skills)
	}
	if c.Expose != "" {
		ports, err := parseExposePorts(c.Expose)
		if err != nil {
			return err
		}
		req["expose"] = ports
	}

	client := newAPIClient()
	var createResp map[string]any
	if err := client.do("POST", "/api/v1/workers", req, &createResp); err != nil {
		return fmt.Errorf("create worker: %w", err)
	}

	if c.NoWait {
		if c.Output == "json" {
			return printJSON(createResp)
		}
		fmt.Printf("worker/%s create accepted (poll `charly agentteams worker get %s -o json` for phase=Running)\n", c.Name, c.Name)
		return nil
	}

	finalStatus, err := waitForWorkerReady(client, c.Name, c.WaitTimeout)
	if err != nil {
		return err
	}
	if c.Output == "json" {
		return printJSON(finalStatus)
	}
	fmt.Printf("worker/%s ready\n", c.Name)
	return nil
}

func waitForWorkerReady(client *apiClient, name string, timeout time.Duration) (*workerResp, error) {
	deadline := time.Now().Add(timeout)
	last := &workerResp{Name: name, Phase: "Pending"}
	for {
		var resp workerResp
		err := client.do("GET", "/api/v1/workers/"+name+"/status", nil, &resp)
		if err == nil {
			last = &resp
			switch resp.Phase {
			case "Ready":
				return &resp, nil
			case "Failed":
				return nil, fmt.Errorf("worker/%s failed during startup: %s", name, renderWorkerStatusSummary(&resp))
			}
		} else if !isRetryableWorkerStatusError(err) {
			return nil, fmt.Errorf("wait for worker/%s ready: %w", name, err)
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("worker/%s did not become ready within %s (last status: %s)", name, timeout, renderWorkerStatusSummary(last))
		}
		time.Sleep(2 * time.Second)
	}
}

func isRetryableWorkerStatusError(err error) bool {
	typed, ok := err.(*apiError)
	if !ok {
		return false
	}
	return typed.StatusCode == 404 || typed.StatusCode >= 500
}

func renderWorkerStatusSummary(resp *workerResp) string {
	if resp == nil {
		return "unknown"
	}
	parts := []string{}
	if phase := strings.TrimSpace(resp.Phase); phase != "" {
		parts = append(parts, "phase="+phase)
	}
	if state := strings.TrimSpace(resp.ContainerState); state != "" {
		parts = append(parts, "state="+state)
	}
	if msg := strings.TrimSpace(resp.Message); msg != "" {
		parts = append(parts, "message="+msg)
	}
	if len(parts) == 0 {
		return "unknown"
	}
	return strings.Join(parts, ", ")
}

type AgentTeamsWorkerUpdateCmd struct {
	Name    string `name:"name" help:"Worker name (required)"`
	Model   string `name:"model" help:"LLM model ID"`
	Runtime string `name:"runtime" help:"Agent runtime (openclaw|copaw|qwenpaw|hermes|openhuman)"`
	Image   string `name:"image" help:"Container image override"`
	Identity string `name:"identity" help:"Worker identity description"`
	Soul    string `name:"soul" help:"Worker SOUL.md content"`
	Skills  string `name:"skills" help:"Comma-separated built-in skills"`
	Package string `name:"package" help:"Package URI"`
	Expose  string `name:"expose" help:"Comma-separated ports to expose"`
	State   string `name:"state" help:"Desired lifecycle state (Running|Sleeping|Stopped)"`
}

func (c *AgentTeamsWorkerUpdateCmd) Run() error {
	if c.Name == "" {
		return fmt.Errorf("--name is required")
	}
	packageURI := c.Package
	if packageURI != "" {
		var err error
		packageURI, err = expandPackageURI(packageURI)
		if err != nil {
			return err
		}
	}
	req := map[string]any{}
	setIfNotEmpty(req, "model", c.Model)
	setIfNotEmpty(req, "runtime", c.Runtime)
	setIfNotEmpty(req, "image", c.Image)
	setIfNotEmpty(req, "identity", c.Identity)
	setIfNotEmpty(req, "soul", c.Soul)
	setIfNotEmpty(req, "package", packageURI)
	setIfNotEmpty(req, "state", c.State)
	if c.Skills != "" {
		req["skills"] = splitCSV(c.Skills)
	}
	if c.Expose != "" {
		ports, err := parseExposePorts(c.Expose)
		if err != nil {
			return err
		}
		req["expose"] = ports
	}
	if len(req) == 0 {
		return fmt.Errorf("at least one field must be specified for update")
	}
	client := newAPIClient()
	var resp map[string]any
	if err := client.do("PUT", "/api/v1/workers/"+c.Name, req, &resp); err != nil {
		return fmt.Errorf("update worker: %w", err)
	}
	fmt.Printf("worker/%s configured\n", c.Name)
	return nil
}

type AgentTeamsWorkerApplyCmd struct {
	Name    string `name:"name" help:"Worker name (required)"`
	Model   string `name:"model" help:"LLM model ID (default: $AGENTTEAMS_DEFAULT_MODEL, else qwen3.6-plus)"`
	Zip     string `name:"zip" help:"Local ZIP package (manifest.json)"`
	Runtime string `name:"runtime" help:"Agent runtime (openclaw|copaw|qwenpaw|hermes|openhuman)"`
	Image   string `name:"image" help:"Container image override"`
	Identity string `name:"identity" help:"Worker identity description"`
	Soul    string `name:"soul" help:"Worker SOUL.md content (inline)"`
	SoulFile string `name:"soul-file" help:"Path to SOUL.md file"`
	Skills  string `name:"skills" help:"Comma-separated built-in skills"`
	Package string `name:"package" help:"Package URI (nacos://, http://, oss://) or shorthand"`
	Expose  string `name:"expose" help:"Comma-separated ports to expose"`
}

func (c *AgentTeamsWorkerApplyCmd) Run() error {
	if c.Name == "" {
		return fmt.Errorf("--name is required")
	}
	if err := validateWorkerName(c.Name); err != nil {
		return err
	}
	if c.Zip != "" {
		return applyWorkerZip(c.Name, c.Zip, c.Runtime)
	}
	return applyWorkerParams(c.Name, c.Model, c.Runtime, c.Image, c.Identity, c.Soul, c.SoulFile, c.Skills, c.Package, c.Expose)
}

// applyWorkerZip uploads a ZIP to the controller, then creates/updates the
// Worker. runtimeOverride wins over whatever the ZIP's manifest declares.
func applyWorkerZip(name, zipPath, runtimeOverride string) error {
	zipData, err := os.ReadFile(zipPath)
	if err != nil {
		return fmt.Errorf("read ZIP %s: %w", zipPath, err)
	}
	model, manifestRuntime := extractWorkerFieldsFromZip(zipData)
	if model == "" {
		model = defaultWorkerModel()
	}
	runtime := runtimeOverride
	if runtime == "" {
		runtime = manifestRuntime
	}

	client := newAPIClient()
	var pkgResp struct {
		PackageUri string `json:"packageUri"`
	}
	if err := client.doMultipart("/api/v1/packages", "file", filepath.Base(zipPath), zipData,
		map[string]string{"name": name}, &pkgResp); err != nil {
		return fmt.Errorf("upload package: %w", err)
	}

	exists, err := client.resourceExists("/api/v1/workers/" + name)
	if err != nil {
		return fmt.Errorf("check worker/%s: %w", name, err)
	}
	var resp map[string]any
	if exists {
		updateBody := map[string]any{"model": model, "package": pkgResp.PackageUri}
		setIfNotEmpty(updateBody, "runtime", runtime)
		if err := client.do("PUT", "/api/v1/workers/"+name, updateBody, &resp); err != nil {
			return fmt.Errorf("update worker/%s: %w", name, err)
		}
		fmt.Printf("  worker/%s updated\n", name)
	} else {
		createBody := map[string]any{"name": name, "model": model, "package": pkgResp.PackageUri}
		setIfNotEmpty(createBody, "runtime", runtime)
		if err := client.do("POST", "/api/v1/workers", createBody, &resp); err != nil {
			return fmt.Errorf("create worker/%s: %w", name, err)
		}
		fmt.Printf("  worker/%s created\n", name)
	}
	return nil
}

// applyWorkerParams creates or updates a Worker from CLI flags (upsert semantics).
func applyWorkerParams(name, model, runtime, image, identity, soul, soulFile, skills, packageURI, expose string) error {
	if model == "" {
		model = defaultWorkerModel()
	}
	if soulFile != "" {
		data, err := os.ReadFile(soulFile)
		if err != nil {
			return fmt.Errorf("read --soul-file %q: %w", soulFile, err)
		}
		soul = string(data)
	}
	if packageURI != "" {
		var err error
		packageURI, err = expandPackageURI(packageURI)
		if err != nil {
			return err
		}
	}
	var exposePorts []map[string]any
	if expose != "" {
		var err error
		exposePorts, err = parseExposePorts(expose)
		if err != nil {
			return err
		}
	}

	client := newAPIClient()
	exists, err := client.resourceExists("/api/v1/workers/" + name)
	if err != nil {
		return fmt.Errorf("check worker/%s: %w", name, err)
	}
	req := map[string]any{"model": model}
	setIfNotEmpty(req, "runtime", runtime)
	setIfNotEmpty(req, "image", image)
	setIfNotEmpty(req, "identity", identity)
	setIfNotEmpty(req, "soul", soul)
	setIfNotEmpty(req, "package", packageURI)
	if skills != "" {
		req["skills"] = splitCSV(skills)
	}
	if expose != "" {
		req["expose"] = exposePorts
	}
	var resp map[string]any
	if exists {
		if err := client.do("PUT", "/api/v1/workers/"+name, req, &resp); err != nil {
			return fmt.Errorf("update worker/%s: %w", name, err)
		}
		fmt.Printf("  worker/%s configured\n", name)
	} else {
		req["name"] = name
		if err := client.do("POST", "/api/v1/workers", req, &resp); err != nil {
			return fmt.Errorf("create worker/%s: %w", name, err)
		}
		fmt.Printf("  worker/%s created\n", name)
	}
	return nil
}

type AgentTeamsWorkerDeleteCmd struct {
	Name string `arg:"" help:"Worker name"`
}

func (c *AgentTeamsWorkerDeleteCmd) Run() error {
	client := newAPIClient()
	if err := client.do("DELETE", "/api/v1/workers/"+c.Name, nil, nil); err != nil {
		return fmt.Errorf("delete worker: %w", err)
	}
	fmt.Printf("worker/%s deleted\n", c.Name)
	return nil
}

// ---------------------------------------------------------------------------
// team
// ---------------------------------------------------------------------------

type AgentTeamsTeamCmd struct {
	Apply  AgentTeamsTeamApplyCmd  `cmd:"" help:"Apply a Team (create-or-update)"`
	List   AgentTeamsTeamListCmd   `cmd:"" help:"List Teams"`
	Get    AgentTeamsTeamGetCmd    `cmd:"" help:"Show one Team"`
	Delete AgentTeamsTeamDeleteCmd `cmd:"" help:"Delete a Team"`
}

type AgentTeamsTeamApplyCmd struct {
	Name                 string `name:"name" help:"Team name (required)"`
	TeamName             string `name:"team-name" help:"Runtime/storage team name (defaults to --name)"`
	LeaderName           string `name:"leader-name" help:"Leader worker name (required)"`
	LeaderHeartbeatEvery string `name:"leader-heartbeat-every" help:"Leader heartbeat interval (e.g. 30m)"`
	Workers              string `name:"workers" help:"Comma-separated existing Worker resource names"`
	Description          string `name:"description" help:"Team description"`
	Admin                string `name:"admin" help:"Existing Human resource used as Team Admin"`
	AdminMatrixID        string `name:"admin-matrix-id" help:"Expected Matrix user ID for the Team Admin"`
	PeerMentions         bool   `name:"peer-mentions" default:"true" help:"Allow Team Workers to mention peers"`
}

func (c *AgentTeamsTeamApplyCmd) Run() error {
	if c.Name == "" {
		return fmt.Errorf("--name is required")
	}
	if c.LeaderName == "" {
		return fmt.Errorf("--leader-name is required")
	}
	workerMembers := []any{map[string]any{"name": c.LeaderName, "role": "team_leader"}}
	if c.Workers != "" {
		for _, w := range splitCSV(c.Workers) {
			workerMembers = append(workerMembers, map[string]any{"name": w, "role": "worker"})
		}
	}
	req := map[string]any{"name": c.Name, "workerMembers": workerMembers}
	setIfNotEmpty(req, "teamName", c.TeamName)
	setIfNotEmpty(req, "description", c.Description)
	setIfNotEmpty(req, "heartbeatEvery", c.LeaderHeartbeatEvery)
	if c.Admin != "" {
		req["admin"] = map[string]any{"name": c.Admin, "matrixUserId": c.AdminMatrixID}
	}
	req["peerMentions"] = c.PeerMentions

	client := newAPIClient()
	exists, err := client.resourceExists("/api/v1/teams/" + c.Name)
	if err != nil {
		return fmt.Errorf("check team/%s: %w", c.Name, err)
	}
	var resp map[string]any
	if exists {
		updateBody := map[string]any{"workerMembers": workerMembers}
		setIfNotEmpty(updateBody, "teamName", c.TeamName)
		setIfNotEmpty(updateBody, "description", c.Description)
		setIfNotEmpty(updateBody, "heartbeatEvery", c.LeaderHeartbeatEvery)
		if c.Admin != "" {
			updateBody["admin"] = map[string]any{"name": c.Admin, "matrixUserId": c.AdminMatrixID}
		}
		updateBody["peerMentions"] = c.PeerMentions
		if err := client.do("PUT", "/api/v1/teams/"+c.Name, updateBody, &resp); err != nil {
			return fmt.Errorf("update team/%s: %w", c.Name, err)
		}
		fmt.Printf("team/%s configured\n", c.Name)
	} else {
		if err := client.do("POST", "/api/v1/teams", req, &resp); err != nil {
			return fmt.Errorf("create team/%s: %w", c.Name, err)
		}
		fmt.Printf("team/%s created\n", c.Name)
	}
	return nil
}

type AgentTeamsTeamListCmd struct {
	Output string `short:"o" name:"output" help:"Output format (json)"`
}

func (c *AgentTeamsTeamListCmd) Run() error {
	client := newAPIClient()
	var resp teamListResp
	if err := client.do("GET", "/api/v1/teams", nil, &resp); err != nil {
		return fmt.Errorf("list teams: %w", err)
	}
	if c.Output == "json" {
		return printJSON(resp)
	}
	if resp.Total == 0 {
		fmt.Println("No teams found.")
		return nil
	}
	rows := make([][]string, 0, len(resp.Teams))
	for _, t := range resp.Teams {
		rows = append(rows, []string{
			t.Name,
			or(t.Phase, "Pending"),
			t.LeaderName,
			strings.Join(t.WorkerNames, ","),
			fmt.Sprintf("%d/%d", t.ReadyWorkers, t.TotalWorkers),
		})
	}
	printTable([]string{"NAME", "PHASE", "LEADER", "WORKERS", "READY"}, rows)
	return nil
}

type AgentTeamsTeamGetCmd struct {
	Name   string `arg:"" help:"Team name"`
	Output string `short:"o" name:"output" help:"Output format (json)"`
}

func (c *AgentTeamsTeamGetCmd) Run() error {
	client := newAPIClient()
	var resp teamResp
	if err := client.do("GET", "/api/v1/teams/"+c.Name, nil, &resp); err != nil {
		return fmt.Errorf("get team: %w", err)
	}
	if c.Output == "json" {
		return printJSON(resp)
	}
	printDetail([]keyValue{
		{"Name", resp.Name},
		{"Phase", or(resp.Phase, "Pending")},
		{"Description", resp.Description},
		{"Leader", resp.LeaderName},
		{"LeaderReady", strconv.FormatBool(resp.LeaderReady)},
		{"Workers", strings.Join(resp.WorkerNames, ", ")},
		{"ReadyWorkers", fmt.Sprintf("%d/%d", resp.ReadyWorkers, resp.TotalWorkers)},
		{"Message", resp.Message},
	})
	return nil
}

type AgentTeamsTeamDeleteCmd struct {
	Name string `arg:"" help:"Team name"`
}

func (c *AgentTeamsTeamDeleteCmd) Run() error {
	client := newAPIClient()
	if err := client.do("DELETE", "/api/v1/teams/"+c.Name, nil, nil); err != nil {
		return fmt.Errorf("delete team: %w", err)
	}
	fmt.Printf("team/%s deleted\n", c.Name)
	return nil
}

// ---------------------------------------------------------------------------
// human
// ---------------------------------------------------------------------------

type AgentTeamsHumanCmd struct {
	Apply  AgentTeamsHumanApplyCmd  `cmd:"" help:"Apply a Human (create-or-update)"`
	List   AgentTeamsHumanListCmd   `cmd:"" help:"List Humans"`
	Get    AgentTeamsHumanGetCmd    `cmd:"" help:"Show one Human"`
	Delete AgentTeamsHumanDeleteCmd `cmd:"" help:"Delete a Human"`
}

type AgentTeamsHumanApplyCmd struct {
	Name              string `name:"name" help:"Human username (required)"`
	DisplayName       string `name:"display-name" help:"Display name (required)"`
	Email             string `name:"email" help:"Email address"`
	PermissionLevel   int    `name:"permission-level" help:"Permission level (0-100)"`
	AccessibleTeams   string `name:"accessible-teams" help:"Comma-separated team names"`
	AccessibleWorkers string `name:"accessible-workers" help:"Comma-separated worker names"`
	Note              string `name:"note" help:"Note for the Human user"`
}

func (c *AgentTeamsHumanApplyCmd) Run() error {
	if c.Name == "" {
		return fmt.Errorf("--name is required")
	}
	if c.DisplayName == "" {
		return fmt.Errorf("--display-name is required")
	}
	req := map[string]any{
		"name":            c.Name,
		"displayName":     c.DisplayName,
		"permissionLevel": c.PermissionLevel,
	}
	setIfNotEmpty(req, "email", c.Email)
	setIfNotEmpty(req, "note", c.Note)
	if c.AccessibleTeams != "" {
		req["accessibleTeams"] = splitCSV(c.AccessibleTeams)
	}
	if c.AccessibleWorkers != "" {
		req["accessibleWorkers"] = splitCSV(c.AccessibleWorkers)
	}

	client := newAPIClient()
	exists, err := client.resourceExists("/api/v1/humans/" + c.Name)
	if err != nil {
		return fmt.Errorf("check human/%s: %w", c.Name, err)
	}
	var resp map[string]any
	if exists {
		updateBody := map[string]any{"displayName": c.DisplayName, "permissionLevel": c.PermissionLevel}
		setIfNotEmpty(updateBody, "email", c.Email)
		setIfNotEmpty(updateBody, "note", c.Note)
		if c.AccessibleTeams != "" {
			updateBody["accessibleTeams"] = splitCSV(c.AccessibleTeams)
		}
		if c.AccessibleWorkers != "" {
			updateBody["accessibleWorkers"] = splitCSV(c.AccessibleWorkers)
		}
		if err := client.do("PUT", "/api/v1/humans/"+c.Name, updateBody, &resp); err != nil {
			return fmt.Errorf("update human/%s: %w", c.Name, err)
		}
		fmt.Printf("human/%s configured\n", c.Name)
	} else {
		if err := client.do("POST", "/api/v1/humans", req, &resp); err != nil {
			return fmt.Errorf("create human/%s: %w", c.Name, err)
		}
		fmt.Printf("human/%s created\n", c.Name)
	}
	return nil
}

type AgentTeamsHumanListCmd struct {
	Output string `short:"o" name:"output" help:"Output format (json)"`
}

func (c *AgentTeamsHumanListCmd) Run() error {
	client := newAPIClient()
	var resp humanListResp
	if err := client.do("GET", "/api/v1/humans", nil, &resp); err != nil {
		return fmt.Errorf("list humans: %w", err)
	}
	if c.Output == "json" {
		return printJSON(resp)
	}
	if resp.Total == 0 {
		fmt.Println("No humans found.")
		return nil
	}
	rows := make([][]string, 0, len(resp.Humans))
	for _, h := range resp.Humans {
		rows = append(rows, []string{
			h.Name,
			or(h.Phase, "Pending"),
			h.DisplayName,
			or(h.MatrixUserID, "-"),
		})
	}
	printTable([]string{"NAME", "PHASE", "DISPLAY-NAME", "MATRIX-ID"}, rows)
	return nil
}

type AgentTeamsHumanGetCmd struct {
	Name   string `arg:"" help:"Human name"`
	Output string `short:"o" name:"output" help:"Output format (json)"`
}

func (c *AgentTeamsHumanGetCmd) Run() error {
	client := newAPIClient()
	var resp humanResp
	if err := client.do("GET", "/api/v1/humans/"+c.Name, nil, &resp); err != nil {
		return fmt.Errorf("get human: %w", err)
	}
	if c.Output == "json" {
		return printJSON(resp)
	}
	printDetail([]keyValue{
		{"Name", resp.Name},
		{"Phase", or(resp.Phase, "Pending")},
		{"DisplayName", resp.DisplayName},
		{"MatrixUserID", resp.MatrixUserID},
		{"InitialPassword", resp.InitialPassword},
		{"Rooms", strings.Join(resp.Rooms, ", ")},
		{"Message", resp.Message},
	})
	return nil
}

type AgentTeamsHumanDeleteCmd struct {
	Name string `arg:"" help:"Human name"`
}

func (c *AgentTeamsHumanDeleteCmd) Run() error {
	client := newAPIClient()
	if err := client.do("DELETE", "/api/v1/humans/"+c.Name, nil, nil); err != nil {
		return fmt.Errorf("delete human: %w", err)
	}
	fmt.Printf("human/%s deleted\n", c.Name)
	return nil
}

// ---------------------------------------------------------------------------
// apply -f <file> — declarative YAML, create-or-update per kind.
// ---------------------------------------------------------------------------

type AgentTeamsApplyCmd struct {
	Files []string `short:"f" name:"file" help:"YAML resource file(s)"`
}

func (c *AgentTeamsApplyCmd) Run() error {
	if len(c.Files) == 0 {
		return fmt.Errorf("at least one -f <file> is required")
	}
	return applyFromFiles(c.Files)
}

type yamlResource struct {
	APIVersion string                 `yaml:"apiVersion"`
	Kind       string                 `yaml:"kind"`
	Metadata   yamlMetadata           `yaml:"metadata"`
	Spec       map[string]interface{} `yaml:"spec"`
}

type yamlMetadata struct {
	Name string `yaml:"name"`
}

func applyFromFiles(files []string) error {
	client := newAPIClient()
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return fmt.Errorf("read %s: %w", f, err)
		}
		docs := splitYAMLDocs(string(data))
		for _, doc := range docs {
			doc = strings.TrimSpace(doc)
			if doc == "" {
				continue
			}
			var res yamlResource
			if err := yaml.Unmarshal([]byte(doc), &res); err != nil {
				return fmt.Errorf("parse YAML in %s: %w", f, err)
			}
			if res.Kind == "" || res.Metadata.Name == "" {
				continue
			}
			if err := applyOneResource(client, res); err != nil {
				return err
			}
		}
	}
	return nil
}

func applyOneResource(client *apiClient, res yamlResource) error {
	kind := strings.ToLower(res.Kind)
	name := res.Metadata.Name
	endpoint := "/api/v1/" + kind + "s"

	exists, err := client.resourceExists(endpoint + "/" + name)
	if err != nil {
		return fmt.Errorf("check %s/%s: %w", kind, name, err)
	}
	var resp map[string]any
	if exists {
		updateBody := buildApplyBody(res, false)
		if err := client.do("PUT", endpoint+"/"+name, updateBody, &resp); err != nil {
			return fmt.Errorf("update %s/%s: %w", kind, name, err)
		}
		fmt.Printf("  %s/%s configured\n", kind, name)
	} else {
		body := buildApplyBody(res, true)
		if err := client.do("POST", endpoint, body, &resp); err != nil {
			return fmt.Errorf("create %s/%s: %w", kind, name, err)
		}
		fmt.Printf("  %s/%s created\n", kind, name)
	}
	return nil
}

func buildApplyBody(res yamlResource, includeName bool) map[string]any {
	body := make(map[string]any)
	if includeName {
		body["name"] = res.Metadata.Name
	}
	for k, v := range res.Spec {
		body[k] = v
	}
	return body
}

func splitYAMLDocs(content string) []string {
	var docs []string
	current := ""
	for _, line := range strings.Split(content, "\n") {
		if strings.TrimSpace(line) == "---" {
			if strings.TrimSpace(current) != "" {
				docs = append(docs, current)
			}
			current = ""
			continue
		}
		current += line + "\n"
	}
	if strings.TrimSpace(current) != "" {
		docs = append(docs, current)
	}
	return docs
}

// ---------------------------------------------------------------------------
// Helpers (mirroring the agt CLI's).
// ---------------------------------------------------------------------------

var workerNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// defaultWorkerModel returns the model ID to use when a CLI flag does not
// specify --model: $AGENTTEAMS_DEFAULT_MODEL when set, else "qwen3.6-plus".
func defaultWorkerModel() string {
	if m := strings.TrimSpace(os.Getenv("AGENTTEAMS_DEFAULT_MODEL")); m != "" {
		return m
	}
	return "qwen3.6-plus"
}

func validateWorkerName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("invalid worker name: name is required")
	}
	if !workerNamePattern.MatchString(name) {
		return fmt.Errorf("invalid worker name %q: must start with a lowercase letter or digit and contain only lowercase letters, digits, and hyphens", name)
	}
	return nil
}

func expandPackageURI(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.Contains(raw, "://") {
		return raw, nil
	}
	base := strings.TrimSpace(os.Getenv("AGENTTEAMS_NACOS_REGISTRY_URI"))
	if base == "" {
		base = "nacos://market.agentteams.io:80/public"
	}
	if !strings.HasPrefix(base, "nacos://") {
		return "", fmt.Errorf("invalid AGENTTEAMS_NACOS_REGISTRY_URI %q: must start with nacos://", base)
	}
	base = strings.TrimRight(base, "/")
	if base == "nacos:" || base == "nacos:/" || base == "nacos://" {
		return "", fmt.Errorf("invalid AGENTTEAMS_NACOS_REGISTRY_URI %q: missing host/namespace", base)
	}
	parts := strings.Split(raw, "/")
	encoded := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return "", fmt.Errorf("invalid package shorthand %q: empty path segment", raw)
		}
		encoded = append(encoded, url.PathEscape(part))
	}
	return base + "/" + strings.Join(encoded, "/"), nil
}

func splitCSV(s string) []string {
	var result []string
	for _, item := range strings.Split(s, ",") {
		item = strings.TrimSpace(item)
		if item != "" {
			result = append(result, item)
		}
	}
	return result
}

func parseExposePorts(s string) ([]map[string]any, error) {
	if strings.TrimSpace(s) == "" {
		return nil, fmt.Errorf("--expose requires at least one port")
	}
	values := strings.Split(s, ",")
	ports := make([]map[string]any, 0, len(values))
	seen := make(map[int]struct{}, len(values))
	for _, raw := range values {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return nil, fmt.Errorf("invalid --expose value %q: port entries must not be empty", s)
		}
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 65535 {
			return nil, fmt.Errorf("invalid --expose port %q: must be an integer between 1 and 65535", raw)
		}
		if _, exists := seen[value]; exists {
			return nil, fmt.Errorf("duplicate --expose port %d", value)
		}
		seen[value] = struct{}{}
		ports = append(ports, map[string]any{"port": value})
	}
	return ports, nil
}

// extractWorkerFieldsFromZip reads manifest.json from the ZIP and extracts the
// model and runtime fields. Both top-level and `worker.<field>` placements are
// honored; the worker block takes precedence. Either return value may be empty.
func extractWorkerFieldsFromZip(zipData []byte) (model, runtime string) {
	r, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return "", ""
	}
	for _, f := range r.File {
		if f.Name != "manifest.json" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return "", ""
		}
		defer func() { _ = rc.Close() }()
		var manifest map[string]interface{}
		if err := json.NewDecoder(rc).Decode(&manifest); err != nil {
			return "", ""
		}
		if m, ok := manifest["model"].(string); ok && m != "" {
			model = m
		}
		if rt, ok := manifest["runtime"].(string); ok && rt != "" {
			runtime = rt
		}
		if w, ok := manifest["worker"].(map[string]interface{}); ok {
			if m, ok := w["model"].(string); ok && m != "" {
				model = m
			}
			if rt, ok := w["runtime"].(string); ok && rt != "" {
				runtime = rt
			}
		}
		return model, runtime
	}
	return "", ""
}

func setIfNotEmpty(m map[string]any, key, value string) {
	if value != "" {
		m[key] = value
	}
}

func or(v, fallback string) string {
	if v != "" {
		return v
	}
	return fallback
}

// ---------------------------------------------------------------------------
// Output helpers.
// ---------------------------------------------------------------------------

type keyValue struct {
	Key   string
	Value string
}

func printJSON(v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal output: %w", err)
	}
	fmt.Println(string(data))
	return nil
}

func printDetail(rows []keyValue) {
	width := 0
	for _, r := range rows {
		if len(r.Key) > width {
			width = len(r.Key)
		}
	}
	for _, r := range rows {
		fmt.Printf("%-*s: %s\n", width, r.Key, r.Value)
	}
}

func printTable(headers []string, rows [][]string) {
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = len(h)
	}
	for _, row := range rows {
		for i, cell := range row {
			if i < len(widths) && len(cell) > widths[i] {
				widths[i] = len(cell)
			}
		}
	}
	var sb strings.Builder
	for i, h := range headers {
		if i > 0 {
			sb.WriteString("  ")
		}
		sb.WriteString(pad(h, widths[i]))
	}
	fmt.Println(sb.String())
	for _, row := range rows {
		sb.Reset()
		for i, cell := range row {
			if i > 0 {
				sb.WriteString("  ")
			}
			sb.WriteString(pad(cell, widths[i]))
		}
		fmt.Println(sb.String())
	}
}

func pad(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(s))
}
