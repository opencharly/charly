// Package testkit provides disposable live protocol fixtures shared by SDK
// consumers. The fixtures are test infrastructure, not production fallbacks.
package testkit

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"

	"golang.org/x/crypto/ssh"
)

type TestingT interface {
	Helper()
	TempDir() string
	Cleanup(func())
	Fatalf(string, ...any)
}

// SSHProcessServer is a real SSH server whose accepted exec session is wired
// byte-for-byte to a child process. It lets consumers prove gRPC-over-SSH with
// the system OpenSSH client without relying on a machine-level sshd fixture.
type SSHProcessServer struct {
	Address      string
	Port         int64
	IdentityFile string
	Home         string
}

func StartSSHProcessServer(t TestingT, process func(string) *exec.Cmd) *SSHProcessServer {
	t.Helper()
	_, hostPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate SSH host key: %v", err)
	}
	hostSigner, err := ssh.NewSignerFromKey(hostPrivate)
	if err != nil {
		t.Fatalf("create SSH host signer: %v", err)
	}
	_, clientPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate SSH client key: %v", err)
	}
	clientSigner, err := ssh.NewSignerFromKey(clientPrivate)
	if err != nil {
		t.Fatalf("create SSH client signer: %v", err)
	}
	privateBlock, err := ssh.MarshalPrivateKey(clientPrivate, "charly disposable transport test")
	if err != nil {
		t.Fatalf("marshal SSH client key: %v", err)
	}
	dir := t.TempDir()
	identity := filepath.Join(dir, "id_ed25519")
	if err := os.WriteFile(identity, pem.EncodeToMemory(privateBlock), 0o600); err != nil {
		t.Fatalf("write SSH client key: %v", err)
	}
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatalf("create SSH config directory: %v", err)
	}
	config := "Host 127.0.0.1\n  StrictHostKeyChecking no\n  UserKnownHostsFile /dev/null\n  LogLevel ERROR\n  IdentitiesOnly yes\n"
	if err := os.WriteFile(filepath.Join(home, ".ssh", "config"), []byte(config), 0o600); err != nil {
		t.Fatalf("write SSH client config: %v", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for SSH fixture: %v", err)
	}
	port, _ := strconv.ParseInt(strconv.Itoa(listener.Addr().(*net.TCPAddr).Port), 10, 64)
	serverConfig := &ssh.ServerConfig{
		PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if string(key.Marshal()) != string(clientSigner.PublicKey().Marshal()) {
				return nil, fmt.Errorf("unrecognized disposable SSH key")
			}
			return nil, nil
		},
	}
	serverConfig.AddHostKey(hostSigner)
	var connections sync.WaitGroup
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			connections.Add(1)
			go func() {
				defer connections.Done()
				serveSSHConnection(conn, serverConfig, process)
			}()
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		<-done
		connections.Wait()
	})
	return &SSHProcessServer{Address: "127.0.0.1", Port: port, IdentityFile: identity, Home: home}
}

func serveSSHConnection(raw net.Conn, config *ssh.ServerConfig, process func(string) *exec.Cmd) {
	connection, channels, requests, err := ssh.NewServerConn(raw, config)
	if err != nil {
		_ = raw.Close()
		return
	}
	defer connection.Close() //nolint:errcheck
	go ssh.DiscardRequests(requests)
	for incoming := range channels {
		if incoming.ChannelType() != "session" {
			_ = incoming.Reject(ssh.UnknownChannelType, "session required")
			continue
		}
		channel, channelRequests, err := incoming.Accept()
		if err != nil {
			continue
		}
		go serveSSHSession(channel, channelRequests, process)
	}
}

func serveSSHSession(channel ssh.Channel, requests <-chan *ssh.Request, process func(string) *exec.Cmd) {
	defer channel.Close() //nolint:errcheck
	for request := range requests {
		if request.Type != "exec" {
			_ = request.Reply(false, nil)
			continue
		}
		var payload struct{ Command string }
		if err := ssh.Unmarshal(request.Payload, &payload); err != nil || payload.Command == "" {
			_ = request.Reply(false, nil)
			return
		}
		if err := request.Reply(true, nil); err != nil {
			return
		}
		cmd := process(payload.Command)
		// Caller-owned os.Pipe ends, assigned directly: os/exec's Wait closes
		// only pipes it created itself (the *Pipe helpers), so Wait can never
		// discard a child's buffered trailing output under a pump that is
		// still reading — the exact loss this fixture used to inflict on
		// trailing stderr ("file already closed" swallowed by io.Copy,
		// surfacing at the client as mysteriously empty stderr). The parent's
		// child-side ends close right after Start (the child holds its own
		// duplicates), so each pump sees EOF when the child exits.
		stdinR, stdinW, err := os.Pipe()
		if err != nil {
			return
		}
		stdoutR, stdoutW, err := os.Pipe()
		if err != nil {
			_ = stdinR.Close()
			_ = stdinW.Close()
			return
		}
		stderrR, stderrW, err := os.Pipe()
		if err != nil {
			_ = stdinR.Close()
			_ = stdinW.Close()
			_ = stdoutR.Close()
			_ = stdoutW.Close()
			return
		}
		cmd.Stdin = stdinR
		cmd.Stdout = stdoutW
		cmd.Stderr = stderrW
		if err := cmd.Start(); err != nil {
			_ = stdinR.Close()
			_ = stdinW.Close()
			_ = stdoutR.Close()
			_ = stdoutW.Close()
			_ = stderrR.Close()
			_ = stderrW.Close()
			return
		}
		_ = stdinR.Close()
		_ = stdoutW.Close()
		_ = stderrW.Close()
		// Drain BOTH output pumps to EOF before sending exit-status: the
		// channel (and its extended-data stream) must carry every trailing
		// byte before the client learns the command finished. Without
		// pumps.Wait the return path's channel.Close could outrun the stderr
		// pump's final write.
		var pumps sync.WaitGroup
		pumps.Add(2)
		go func() { defer pumps.Done(); _, _ = io.Copy(channel, stdoutR); _ = stdoutR.Close() }()
		go func() { defer pumps.Done(); _, _ = io.Copy(channel.Stderr(), stderrR); _ = stderrR.Close() }()
		go func() { _, _ = io.Copy(stdinW, channel); _ = stdinW.Close() }()
		waitErr := cmd.Wait()
		pumps.Wait()
		status := uint32(0)
		if waitErr != nil {
			status = 255
			var exitErr *exec.ExitError
			if errors.As(waitErr, &exitErr) && exitErr.ExitCode() >= 0 {
				status = uint32(exitErr.ExitCode())
			}
		}
		_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{status}))
		return
	}
}
