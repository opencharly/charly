package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"time"

	"github.com/opencharly/charly/charly/plugin/kit"
	pb "github.com/opencharly/charly/charly/plugin/proto"
	"github.com/opencharly/charly/charly/spec"
)

// ServeCheckVerb serves a HOST-COUPLED check verb (kit.CheckVerbProvider) OUT-OF-PROCESS
// (F2): it wraps the verb in a pb.ProviderServer whose Invoke reconstructs a kit.CheckContext
// from the host's reverse channel (ExecutorService for Exec + CheckContextService for
// HTTPDo/AddBackground, on the InvokeRequest broker) plus the env_json CheckEnv snapshot
// (Mode/Box/Instance/Distros/DialTimeout), runs RunVerb, and returns the verdict. The SAME
// kit verb compiles INTO charly in-process (registerCompiledCheckVerb passes the live *Runner
// as the CheckContext); this is the out-of-process placement, ZERO authoring change. A kit
// candy's cmd/serve is a one-liner: sdk.ServeCheckVerb(pkg.NewCheckVerb(), calver,
// pkg.SchemaFS, pkg.SchemaDir, pkg.InputDefs).
func ServeCheckVerb(kv kit.CheckVerbProvider, calver string, schemaFS fs.FS, schemaDir string, inputDefs map[string]string) {
	Serve(&checkVerbServer{kv: kv}, &checkVerbMeta{kv: kv, calver: calver, schemaFS: schemaFS, schemaDir: schemaDir, inputDefs: inputDefs})
}

// checkVerbServer is the pb.ProviderServer that runs a kit verb out-of-process.
type checkVerbServer struct {
	pb.UnimplementedProviderServer
	kv kit.CheckVerbProvider
}

func kitStatusWire(s kit.Status) string {
	switch s {
	case kit.StatusFail:
		return "fail"
	case kit.StatusSkip:
		return "skip"
	default:
		return "pass"
	}
}

func (s *checkVerbServer) Invoke(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	var op spec.Op
	if len(req.GetParamsJson()) > 0 {
		if err := json.Unmarshal(req.GetParamsJson(), &op); err != nil {
			return ResultJSON("fail", "check verb: decode op: "+err.Error())
		}
	}
	var env checkEnvWire
	if len(req.GetEnvJson()) > 0 {
		_ = json.Unmarshal(req.GetEnvJson(), &env)
	}
	cc, err := newSDKCheckContext(req.GetExecutorBrokerId(), env)
	if err != nil {
		return ResultJSON("fail", "check verb: reverse channel: "+err.Error())
	}
	res := s.kv.RunVerb(ctx, cc, &op)
	return ResultJSON(kitStatusWire(res.Status), res.Message)
}

func (s *checkVerbServer) InvokeStream(req *pb.InvokeRequest, stream pb.Provider_InvokeStreamServer) error {
	rep, err := s.Invoke(stream.Context(), req)
	if err != nil {
		return err
	}
	return stream.Send(&pb.Frame{ResultJson: rep.GetResultJson()})
}

// checkVerbMeta is the pb.PluginMetaServer half: Describe advertises the verb capability +
// serves its CUE schema (BuildCapabilities compiles it standalone, failing loudly).
type checkVerbMeta struct {
	pb.UnimplementedPluginMetaServer
	kv        kit.CheckVerbProvider
	calver    string
	schemaFS  fs.FS
	schemaDir string
	inputDefs map[string]string
}

func (m *checkVerbMeta) Describe(context.Context, *pb.Empty) (*pb.Capabilities, error) {
	return BuildCapabilities(m.calver,
		[]ProvidedCapability{{Class: "verb", Word: m.kv.Reserved(), InputDef: m.inputDefs["verb:"+m.kv.Reserved()]}},
		m.schemaFS, m.schemaDir)
}

// checkEnvWire is the plugin-side decode of the host's CheckEnv (charly/provider_checkenv.go)
// for the scalar kit.CheckContext legs that ride the env_json snapshot rather than the
// reverse channel. Only the kit-consumed fields are decoded.
type checkEnvWire struct {
	Box           string   `json:"box"`
	Instance      string   `json:"instance"`
	Mode          string   `json:"mode"`
	Distros       []string `json:"distros"`
	VenueKind     string   `json:"venue_kind"`
	DialTimeoutNs int64    `json:"dial_timeout_ns"`
}

// newSDKCheckContext builds the kit.CheckContext an out-of-process kit verb consumes: Exec
// over ExecutorService, HTTPDo/AddBackground over CheckContextService, and the scalar legs from
// the env snapshot. It dials the broker exactly ONCE: the host serves BOTH reverse services on
// the SAME broker id (one grpc.Server via InvokeWithExecutor), and go-plugin's GRPCBroker
// pairs ONE Dial with ONE AcceptAndServe per id — a second Dial would hang ("timeout waiting
// for connection info"). gRPC multiplexes both service clients on the single conn.
func newSDKCheckContext(brokerID uint32, env checkEnvWire) (kit.CheckContext, error) {
	if servedBroker == nil {
		return nil, errors.New("sdk: no go-plugin broker (plugin not served over go-plugin)")
	}
	if brokerID == 0 {
		return nil, errors.New("sdk: no host reverse channel attached (executor_broker_id=0)")
	}
	conn, err := servedBroker.Dial(brokerID)
	if err != nil {
		return nil, err
	}
	return &sdkCheckContext{
		exec: &Executor{client: pb.NewExecutorServiceClient(conn)},
		cc:   pb.NewCheckContextServiceClient(conn),
		env:  env,
	}, nil
}

// sdkCheckContext is the OUT-OF-PROCESS kit.CheckContext: the plugin-side twin of charly's
// in-proc runnerCheckContext, backed by the reverse channel + the env snapshot.
type sdkCheckContext struct {
	exec *Executor
	cc   pb.CheckContextServiceClient
	env  checkEnvWire
}

func (c *sdkCheckContext) Exec() kit.Executor {
	return &sdkKitExecutor{e: c.exec, kind: c.env.VenueKind}
}

func (c *sdkCheckContext) Mode() kit.RunMode {
	if c.env.Mode == "box" {
		return kit.ModeBox
	}
	return kit.ModeLive
}

func (c *sdkCheckContext) Box() string                { return c.env.Box }
func (c *sdkCheckContext) Instance() string           { return c.env.Instance }
func (c *sdkCheckContext) Distros() []string          { return c.env.Distros }
func (c *sdkCheckContext) DialTimeout() time.Duration { return time.Duration(c.env.DialTimeoutNs) }

func (c *sdkCheckContext) HTTPDo(ctx context.Context, req kit.HTTPRequest) (kit.HTTPResponse, error) {
	rep, err := c.cc.HTTPDo(ctx, &pb.HTTPDoRequest{
		Method:            req.Method,
		Url:               req.URL,
		Body:              req.Body,
		Headers:           req.Headers,
		Timeout:           req.Timeout,
		AllowInsecure:     req.AllowInsecure,
		NoFollowRedirects: req.NoFollowRedirects,
		CaPem:             req.CAPEM,
	})
	if err != nil {
		return kit.HTTPResponse{}, err
	}
	if rep.GetError() != "" {
		return kit.HTTPResponse{}, errors.New(rep.GetError())
	}
	return kit.HTTPResponse{Status: int(rep.GetStatus()), Body: rep.GetBody(), HeaderBlob: rep.GetHeaderBlob()}, nil
}

func (c *sdkCheckContext) AddBackground(pid int) {
	_, _ = c.cc.AddBackground(context.Background(), &pb.AddBackgroundRequest{Pid: int32(pid)})
}

// sdkKitExecutor adapts the plugin-side *sdk.Executor to kit.Executor (RunCapture over the
// reverse channel; Kind from the env's venue_kind, since the executor's Kind is a host fact).
type sdkKitExecutor struct {
	e    *Executor
	kind string
}

func (x *sdkKitExecutor) RunCapture(ctx context.Context, script string) (string, string, int, error) {
	return x.e.RunCapture(ctx, script)
}

func (x *sdkKitExecutor) Kind() string { return x.kind }
