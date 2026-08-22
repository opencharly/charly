package exec

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/opencharly/spec/proc"
	"github.com/opencharly/spec/spec"
)

// commandProcess is the shared lifecycle implementation for exact-argv
// processes started by ShellExecutor, SSHExecutor, and NestedExecutor.
type commandProcess struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr io.ReadCloser
	done   chan struct{}

	mu      sync.Mutex
	waitErr error
	close   sync.Once
}

func startCommandProcess(cmd *exec.Cmd) (spec.Process, error) {
	bindProcessGroupKill(cmd)
	pipes, err := startProcessPipes(cmd)
	if err != nil {
		return nil, err
	}
	p := &commandProcess{cmd: cmd, stdin: pipes.stdin, stdout: pipes.stdout, stderr: pipes.stderr, done: make(chan struct{})}
	go func() {
		err := cmd.Wait()
		p.mu.Lock()
		p.waitErr = err
		p.mu.Unlock()
		close(p.done)
	}()
	return p, nil
}

func (p *commandProcess) Stdin() io.WriteCloser { return p.stdin }
func (p *commandProcess) Stdout() io.ReadCloser { return p.stdout }
func (p *commandProcess) Stderr() io.ReadCloser { return p.stderr }

func (p *commandProcess) Wait() error {
	<-p.done
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.waitErr
}

func (p *commandProcess) Close() error {
	p.close.Do(func() {
		proc.ShutdownProcessGroup(p.cmd, p.stdin, p.done)
	})
	err := p.Wait()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return nil
	}
	return err
}

func validateProcessLaunch(launch spec.ProcessLaunch) error {
	argv := launch.Argv
	if len(argv) == 0 || argv[0] == "" {
		return errors.New("process argv must name an executable")
	}
	for i, arg := range argv {
		if containsNUL(arg) {
			return fmt.Errorf("process argv[%d] contains NUL", i)
		}
	}
	for key := range launch.Env {
		if key == "" || strings.ContainsAny(key, "=\x00") {
			return fmt.Errorf("process environment key %q is invalid", key)
		}
	}
	return nil
}

func containsNUL(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == 0 {
			return true
		}
	}
	return false
}

// StartProcess launches argv directly on the local venue. No shell is involved.
func (ShellExecutor) StartProcess(ctx context.Context, launch spec.ProcessLaunch) (spec.Process, error) {
	if err := validateProcessLaunch(launch); err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, launch.Argv[0], launch.Argv[1:]...)
	cmd.Dir = launch.WorkingDir
	cmd.Env = append(os.Environ(), proc.SortedEnvPairs(launch.Env)...)
	return startCommandProcess(cmd)
}

// StartProcess launches one exact argv through SSH. OpenSSH necessarily sends a
// remote command string; POSIX single quoting each token preserves the argv
// boundary and prevents interpolation by the remote login shell.
func (e *SSHExecutor) StartProcess(ctx context.Context, launch spec.ProcessLaunch) (spec.Process, error) {
	if err := validateProcessLaunch(launch); err != nil {
		return nil, err
	}
	args := e.SSHBaseArgs()
	args = append(args, proc.RemoteLaunchCommand(launch))
	return startCommandProcess(exec.CommandContext(ctx, "ssh", args...))
}

// StartProcess composes the leaf argv into one argv for the parent process
// executor. This makes arbitrary container/SSH nesting recursive and keeps the
// gRPC transport unaware of deployment kinds.
func (n *NestedExecutor) StartProcess(ctx context.Context, launch spec.ProcessLaunch) (spec.Process, error) {
	if err := validateProcessLaunch(launch); err != nil {
		return nil, err
	}
	if n.Parent == nil {
		return nil, errors.New("NestedExecutor: nil Parent")
	}
	parent, ok := n.Parent.(spec.ProcessExecutor)
	if !ok {
		return nil, fmt.Errorf("NestedExecutor parent %q: %w", n.Parent.Venue(), spec.ErrNotSupported)
	}
	var outer []string
	switch n.Jump.Kind {
	case JumpPodmanExec:
		outer = append([]string{"podman", "exec", "-i"}, n.Jump.ExtraArgs...)
		if launch.WorkingDir != "" {
			outer = append(outer, "--workdir", launch.WorkingDir)
		}
		for _, pair := range proc.SortedEnvPairs(launch.Env) {
			outer = append(outer, "--env", pair)
		}
		outer = append(outer, n.Jump.Target)
	case JumpDockerExec:
		outer = append([]string{"docker", "exec", "-i"}, n.Jump.ExtraArgs...)
		if launch.WorkingDir != "" {
			outer = append(outer, "--workdir", launch.WorkingDir)
		}
		for _, pair := range proc.SortedEnvPairs(launch.Env) {
			outer = append(outer, "--env", pair)
		}
		outer = append(outer, n.Jump.Target)
	case JumpSSH:
		outer = append([]string{"ssh", "-T"}, nestedSSHLogArgs()...)
		outer = append(outer, n.Jump.ExtraArgs...)
		outer = append(outer, n.Jump.Target)
		outer = append(outer, proc.RemoteLaunchCommand(launch))
	case JumpVirshConsole:
		return nil, fmt.Errorf("NestedExecutor process over virsh console: %w", spec.ErrNotSupported)
	default:
		return nil, fmt.Errorf("NestedExecutor process jump %d: %w", n.Jump.Kind, spec.ErrNotSupported)
	}
	if n.Jump.Kind == JumpPodmanExec || n.Jump.Kind == JumpDockerExec {
		outer = append(outer, launch.Argv...)
	}
	return parent.StartProcess(ctx, spec.ProcessLaunch{Argv: outer})
}
