// Package transport (github.com/opencharly/spec/transport, #55 step1) provides
// transport-neutral gRPC connections to Charly targets. It is a fabric slice of
// the spec contract module — a host primitive a plugin cannot hold as a shared
// library (the gRPC dial/serve over a process stdio carrier) — so by #55 Rule 1
// it lives in a contained spec slice whose only heavy dep is
// google.golang.org/grpc + spec/proto (Rule 2). SSH is only a process transport:
// gRPC still owns the protocol, and the target chain carried by CUE-generated
// spec.TargetSpec owns nested routing.
package transport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/opencharly/spec/proc"
	pb "github.com/opencharly/spec/proto"
	"github.com/opencharly/spec/spec"
)

const (
	defaultCharlyBinary = "charly"
	stdioServeGroup     = "__agent-target"
	stdioServeLeaf      = "serve"
)

// DialOptions controls process launch only. It deliberately has no runtime- or
// tmux-specific fields; those remain in the CUE payload sent over Provider.Channel.
type DialOptions struct {
	CharlyBinary       string
	RemoteCharlyBinary string
	SSHBinary          string
	Stderr             io.Writer
	ExtraGRPC          []grpc.DialOption
	Command            func(context.Context, string, ...string) *exec.Cmd
}

// DialProvider opens gRPC over a local-exec or SSH stdio process. The SSH
// remote argv is fixed to `charly __agent-target serve --stdio`; nested target
// hops are carried as TargetSpec on the first Provider.Channel frame and are
// resolved by the responsible Charly node.
func DialProvider(ctx context.Context, target spec.TargetSpec, opts DialOptions) (*grpc.ClientConn, pb.ProviderClient, error) {
	launch, err := stdioLaunchPlan(target, opts)
	if err != nil {
		return nil, nil, err
	}
	command := opts.Command
	if command == nil {
		command = exec.CommandContext
	}
	cmd := command(ctx, launch.argv[0], launch.argv[1:]...)
	cmd.Dir = launch.dir
	if launch.env != nil {
		cmd.Env = append(cmd.Environ(), launch.env...)
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// The pipes are created with os.Pipe and assigned directly, so they belong
	// to the caller: os/exec's Wait closes only pipes it created itself (the
	// StdinPipe/StdoutPipe helpers), which is exactly the race its docs warn
	// about — "incorrect to call Wait before all reads from the pipe have
	// completed". Here the Wait monitor may run concurrently with the gRPC
	// reader without ever closing stdout under it ("read |0: file already
	// closed"). The parent's copies of the child-side ends are closed as soon
	// as Start succeeds (the child holds its own duplicates), so the reader
	// sees EOF the instant the carrier exits.
	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		return nil, nil, err
	}
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		_ = stdinR.Close()
		_ = stdinW.Close()
		return nil, nil, err
	}
	cmd.Stdin = stdinR
	cmd.Stdout = stdoutW
	if opts.Stderr != nil {
		cmd.Stderr = opts.Stderr
	}
	if err := cmd.Start(); err != nil {
		_ = stdinR.Close()
		_ = stdinW.Close()
		_ = stdoutR.Close()
		_ = stdoutW.Close()
		return nil, nil, err
	}
	_ = stdinR.Close()
	_ = stdoutW.Close()
	processConn := newProcessConn(cmd, stdoutR, stdinW)
	return dialProviderConn(ctx, processConn, opts.ExtraGRPC)
}

// DialProcessProvider opens gRPC over a process supplied by a deployment
// executor. This is the placement-neutral sibling of DialProvider: the same
// protocol runs over local exec, SSH, containers, VMs, and recursively nested
// venues without teaching the gRPC layer about any of them.
func DialProcessProvider(ctx context.Context, process spec.Process, opts DialOptions) (*grpc.ClientConn, pb.ProviderClient, error) {
	if process == nil {
		return nil, nil, errors.New("targetkit: nil process")
	}
	stderr := opts.Stderr
	if stderr == nil {
		stderr = io.Discard
	}
	go func() { _, _ = io.Copy(stderr, process.Stderr()) }()
	processConn := newExternalProcessConn(process)
	return dialProviderConn(ctx, processConn, opts.ExtraGRPC)
}

func dialProviderConn(ctx context.Context, processConn net.Conn, extra []grpc.DialOption) (*grpc.ClientConn, pb.ProviderClient, error) {
	dialOpts := make([]grpc.DialOption, 0, 2+len(extra))
	dialOpts = append(dialOpts,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return processConn, nil }),
	)
	dialOpts = append(dialOpts, extra...)
	conn, err := grpc.DialContext(ctx, "passthrough:///charly-target-stdio", dialOpts...)
	if err != nil {
		_ = processConn.Close()
		return nil, nil, err
	}
	return conn, pb.NewProviderClient(conn), nil
}

// StdioCommand validates the generic route and returns an argv with no shell
// interpolation. Hops after the first grpc marker—including tmux, another SSH
// node, or an exec/container hop—are nested routing data, not special cases.
func StdioCommand(target spec.TargetSpec, opts DialOptions) ([]string, error) {
	launch, err := stdioLaunchPlan(target, opts)
	if err != nil {
		return nil, err
	}
	return launch.argv, nil
}

type stdioLaunch struct {
	argv []string
	dir  string
	env  []string
}

// stdioLaunchPlan keeps endpoint attributes on the side that owns them. An
// exec hop launches the endpoint directly, so cwd/env belong to the local
// child. An SSH hop renders cwd/env into the remote command; applying either
// to the local ssh carrier would conflate controller and target state.
func stdioLaunchPlan(target spec.TargetSpec, opts DialOptions) (stdioLaunch, error) {
	if len(target.Hops) == 0 {
		return stdioLaunch{}, errors.New("targetkit: target route has no hops")
	}
	grpcAt := -1
	for i, hop := range target.Hops {
		if hop.Transport == "grpc" {
			grpcAt = i
			break
		}
	}
	if grpcAt < 0 {
		return stdioLaunch{}, errors.New("targetkit: target route requires a grpc hop")
	}
	first := target.Hops[0]
	charly := opts.CharlyBinary
	if charly == "" {
		charly = defaultCharlyBinary
	}
	switch first.Transport {
	case "exec":
		if grpcAt != 1 && grpcAt != 0 {
			return stdioLaunch{}, fmt.Errorf("targetkit: exec transport must connect directly to grpc, got grpc at hop %d", grpcAt)
		}
		argv := []string{charly, stdioServeGroup, stdioServeLeaf, "--stdio"}
		if len(first.Command) > 0 {
			argv = append([]string(nil), first.Command...)
		}
		launch := stdioLaunch{argv: argv, dir: target.WorkingDir}
		if len(first.Env) > 0 {
			launch.env = proc.SortedEnvPairs(first.Env)
		}
		return launch, nil
	case "ssh":
		if first.Address == "" {
			return stdioLaunch{}, errors.New("targetkit: ssh hop requires address")
		}
		ssh := opts.SSHBinary
		if ssh == "" {
			ssh = "ssh"
		}
		argv := []string{ssh, "-T"}
		if first.Port != 0 {
			argv = append(argv, "-p", strconv.FormatInt(first.Port, 10))
		}
		if first.IdentityFile != "" {
			argv = append(argv, "-i", first.IdentityFile)
		}
		for _, option := range proc.SortedEnvPairs(first.Options) {
			argv = append(argv, "-o", option)
		}
		destination := first.Address
		if first.User != "" {
			destination = first.User + "@" + destination
		}
		argv = append(argv, destination)
		// CharlyBinary belongs to the controller placement and may be an absolute
		// worktree path, so it is never sent across SSH. RemoteCharlyBinary is an
		// explicitly target-side path returned by Charly's replication mechanism;
		// absent that, the remote box resolves an installed endpoint through PATH.
		remoteCharly := opts.RemoteCharlyBinary
		if remoteCharly == "" {
			remoteCharly = defaultCharlyBinary
		}
		remote := proc.RemoteLaunchCommand(spec.ProcessLaunch{
			Argv:       []string{remoteCharly, stdioServeGroup, stdioServeLeaf, "--stdio"},
			WorkingDir: target.WorkingDir,
			Env:        first.Env,
		})
		argv = append(argv, remote)
		return stdioLaunch{argv: argv}, nil
	default:
		return stdioLaunch{}, fmt.Errorf("targetkit: outer transport %q cannot carry gRPC stdio", first.Transport)
	}
}

// ServeStdio runs a gRPC server on one full-duplex stdio connection. stdout is
// exclusively HTTP/2 protocol data; diagnostics belong on stderr.
func ServeStdio(stdin io.ReadCloser, stdout io.WriteCloser, register func(*grpc.Server), opts ...grpc.ServerOption) error {
	conn := &stdioConn{Reader: stdin, Writer: stdout, local: stdioAddr("server"), remote: stdioAddr("controller")}
	listener := newSingleConnListener(conn)
	server := grpc.NewServer(opts...)
	register(server)
	err := server.Serve(listener)
	if errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}

type stdioConn struct {
	io.Reader
	io.Writer
	local, remote net.Addr
	closeOnce     sync.Once
	closeFn       func()
}

func (c *stdioConn) Close() error {
	c.closeOnce.Do(func() {
		if c.closeFn != nil {
			c.closeFn()
		}
	})
	return nil
}
func (c *stdioConn) LocalAddr() net.Addr              { return c.local }
func (c *stdioConn) RemoteAddr() net.Addr             { return c.remote }
func (c *stdioConn) SetDeadline(time.Time) error      { return nil }
func (c *stdioConn) SetReadDeadline(time.Time) error  { return nil }
func (c *stdioConn) SetWriteDeadline(time.Time) error { return nil }

type stdioAddr string

func (a stdioAddr) Network() string { return "stdio" }
func (a stdioAddr) String() string  { return string(a) }

type processConn struct {
	*stdioConn
	cmd  *exec.Cmd
	done chan struct{}
}

type externalProcessConn struct {
	*stdioConn
	process spec.Process
}

func newExternalProcessConn(process spec.Process) *externalProcessConn {
	p := &externalProcessConn{process: process}
	p.stdioConn = &stdioConn{
		Reader: process.Stdout(), Writer: process.Stdin(),
		local: stdioAddr("controller"), remote: stdioAddr("target"),
		closeFn: func() { _ = process.Close() },
	}
	return p
}

func newProcessConn(cmd *exec.Cmd, stdout io.ReadCloser, stdin io.WriteCloser) *processConn {
	p := &processConn{cmd: cmd, done: make(chan struct{})}
	p.stdioConn = &stdioConn{Reader: stdout, Writer: stdin, local: stdioAddr("controller"), remote: stdioAddr("target")}
	p.closeFn = func() { proc.ShutdownProcessGroup(cmd, stdin, p.done) }
	go func() { _ = cmd.Wait(); close(p.done) }()
	return p
}

type singleConnListener struct {
	conn      net.Conn
	once      sync.Once
	closed    chan struct{}
	closeOnce sync.Once
}

func newSingleConnListener(conn *stdioConn) *singleConnListener {
	l := &singleConnListener{conn: conn, closed: make(chan struct{})}
	conn.closeFn = func() { l.closeOnce.Do(func() { close(l.closed) }) }
	return l
}

func (l *singleConnListener) Accept() (net.Conn, error) {
	accepted := false
	l.once.Do(func() {
		accepted = true
	})
	if accepted {
		return l.conn, nil
	}
	<-l.closed
	return nil, net.ErrClosed
}
func (l *singleConnListener) Close() error {
	l.closeOnce.Do(func() { close(l.closed) })
	return l.conn.Close()
}
func (l *singleConnListener) Addr() net.Addr { return stdioAddr("listener") }

// StdioFiles is the production adapter used by the hidden target command.
func StdioFiles() (io.ReadCloser, io.WriteCloser) { return os.Stdin, os.Stdout }
