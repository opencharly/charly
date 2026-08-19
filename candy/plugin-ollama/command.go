package ollama

// command.go — the entire `charly ollama` CLI: the subcommand grammar and the Ollama HTTP
// API calls, built on the official upstream client (github.com/ollama/ollama/api). The
// plugin is deliberately lightweight: one file, a hand-rolled token switch (the
// plugin-candy pattern — no Kong inside the plugin), and thin wrappers over the api.Client
// methods. Endpoint resolution: --server flag > OLLAMA_HOST env > http://127.0.0.1:11434.

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/ollama/ollama/api"
)

const defaultHost = "http://127.0.0.1:11434"

const usage = `charly ollama — manage a deployed Ollama server over its HTTP API

Usage: charly ollama <command> [args] [--server <url>]

Commands:
  list, ls              List pulled models
  ps                    List models currently loaded in memory
  pull <model>          Pull a model from the registry
  rm <model>...         Remove one or more models
  show <model>          Show a model's details and Modelfile
  cp <src> <dst>        Copy a model
  create <name> --from <base> [--system <text>]
                        Create a model from a base model
  push <model>          Push a model to the registry
  run <model> [prompt…] Generate a completion (interactive chat when no prompt)
  stop <model>          Unload a model from memory
  version               Print the server version
  help                  Show this help

Endpoint resolution: --server flag > OLLAMA_HOST env > ` + defaultHost + `

For full Modelfile authoring use the upstream CLI inside the deployment
(the ollama candy's host alias: charly alias install ollama).`

// options carries the global flags.
type options struct {
	host string
}

// runOllamaCLI is the shared effect behind Invoke(OpRun) and CliMain.
func runOllamaCLI(args []string) error {
	opts, rest := parseGlobalFlags(args)
	if len(rest) == 0 {
		fmt.Println(usage)
		return nil
	}
	sub, subArgs := rest[0], rest[1:]
	switch sub {
	case "help", "-h", "--help":
		fmt.Println(usage)
		return nil
	case "list", "ls":
		return cmdList(clientFrom(opts), subArgs)
	case "ps":
		return cmdPS(clientFrom(opts), subArgs)
	case "pull":
		return cmdPull(clientFrom(opts), subArgs)
	case "rm", "delete":
		return cmdRemove(clientFrom(opts), subArgs)
	case "show":
		return cmdShow(clientFrom(opts), subArgs)
	case "cp", "copy":
		return cmdCopy(clientFrom(opts), subArgs)
	case "create":
		return cmdCreate(clientFrom(opts), subArgs)
	case "push":
		return cmdPush(clientFrom(opts), subArgs)
	case "run":
		return cmdRun(clientFrom(opts), subArgs)
	case "stop":
		return cmdStop(clientFrom(opts), subArgs)
	case "version":
		return cmdVersion(clientFrom(opts), subArgs)
	default:
		return fmt.Errorf("unknown ollama command %q\n\n%s", sub, usage)
	}
}

// parseGlobalFlags extracts --server/-s (either "--server url" or "--server=url") from
// anywhere in the arg list and returns the remaining positionals. The flag is named
// --server, NOT --host: the charly-level Kong grammar already owns --host (SSH remote
// execution) for every command.
func parseGlobalFlags(args []string) (options, []string) {
	var opts options
	var rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--server" || a == "-s":
			if i+1 < len(args) {
				opts.host = args[i+1]
				i++
			}
		case strings.HasPrefix(a, "--server="):
			opts.host = strings.TrimPrefix(a, "--server=")
		case strings.HasPrefix(a, "-s="):
			opts.host = strings.TrimPrefix(a, "-s=")
		default:
			rest = append(rest, a)
		}
	}
	return opts, rest
}

// clientFrom builds the api.Client for the resolved endpoint.
func clientFrom(opts options) *api.Client {
	u, err := url.Parse(resolveHost(opts.host))
	if err != nil { // resolveHost only emits well-formed URLs; fall back to the default.
		u, _ = url.Parse(defaultHost)
	}
	return api.NewClient(u, http.DefaultClient)
}

// resolveHost implements flag > env > default and normalizes the result into a URL the
// client can dial: a scheme is prepended when missing, and a 0.0.0.0/empty hostname (the
// ollama candy's server-bind value) maps to 127.0.0.1 for client use.
func resolveHost(flagHost string) string {
	h := flagHost
	if h == "" {
		h = os.Getenv("OLLAMA_HOST")
	}
	if h == "" {
		return defaultHost
	}
	h = strings.TrimSuffix(strings.TrimSpace(h), "/")
	if !strings.Contains(h, "://") {
		h = "http://" + h
	}
	u, err := url.Parse(h)
	if err != nil {
		return defaultHost
	}
	if u.Hostname() == "" || u.Hostname() == "0.0.0.0" {
		port := u.Port()
		u.Host = "127.0.0.1"
		if port != "" {
			u.Host = "127.0.0.1:" + port
		}
	}
	return u.String()
}

// --- list / ps -------------------------------------------------------------

func cmdList(client *api.Client, args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("usage: charly ollama list")
	}
	resp, err := client.List(context.Background())
	if err != nil {
		return fmt.Errorf("ollama list: %w", err)
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tSIZE\tMODIFIED")
	for _, m := range resp.Models {
		fmt.Fprintf(w, "%s\t%s\t%s\n", m.Name, humanSize(m.Size), humanTime(time.Until(m.ModifiedAt)))
	}
	return w.Flush()
}

func cmdPS(client *api.Client, args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("usage: charly ollama ps")
	}
	resp, err := client.ListRunning(context.Background())
	if err != nil {
		return fmt.Errorf("ollama ps: %w", err)
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tSIZE\tVRAM\tUNTIL")
	for _, m := range resp.Models {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", m.Name, humanSize(m.Size), humanSize(m.SizeVRAM), humanTime(time.Until(m.ExpiresAt)))
	}
	return w.Flush()
}

// --- pull / push / create --------------------------------------------------

// progress renders a compact one-line-per-stage transfer progress on stderr.
type progress struct {
	last string
}

func (p *progress) fn(resp api.ProgressResponse) error {
	if resp.Total > 0 {
		pct := resp.Completed * 100 / resp.Total
		fmt.Fprintf(os.Stderr, "\r%s %d%% (%s/%s)   ", resp.Status, pct, humanSize(resp.Completed), humanSize(resp.Total))
		p.last = resp.Status
		return nil
	}
	if resp.Status != p.last {
		if p.last != "" {
			fmt.Fprintln(os.Stderr)
		}
		fmt.Fprintln(os.Stderr, resp.Status)
		p.last = resp.Status
	}
	return nil
}

func (p *progress) done() {
	if p.last != "" {
		fmt.Fprintln(os.Stderr)
	}
}

func cmdPull(client *api.Client, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: charly ollama pull <model>")
	}
	p := &progress{}
	err := client.Pull(context.Background(), &api.PullRequest{Model: args[0]}, p.fn)
	p.done()
	if err != nil {
		return fmt.Errorf("ollama pull %s: %w", args[0], err)
	}
	fmt.Printf("pulled %s\n", args[0])
	return nil
}

func cmdPush(client *api.Client, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: charly ollama push <model>")
	}
	p := &progress{}
	err := client.Push(context.Background(), &api.PushRequest{Model: args[0]}, p.fn)
	p.done()
	if err != nil {
		return fmt.Errorf("ollama push %s: %w", args[0], err)
	}
	fmt.Printf("pushed %s\n", args[0])
	return nil
}

func cmdCreate(client *api.Client, args []string) error {
	var name, from, system string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--from":
			if i+1 < len(args) {
				from = args[i+1]
				i++
			}
		case "--system":
			if i+1 < len(args) {
				system = args[i+1]
				i++
			}
		default:
			if name == "" {
				name = args[i]
			} else {
				return fmt.Errorf("usage: charly ollama create <name> --from <base> [--system <text>]")
			}
		}
	}
	if name == "" || from == "" {
		return fmt.Errorf("usage: charly ollama create <name> --from <base> [--system <text>]\n\nfull Modelfile authoring stays with the upstream CLI inside the deployment")
	}
	p := &progress{}
	req := &api.CreateRequest{Model: name, From: from, System: system}
	err := client.Create(context.Background(), req, p.fn)
	p.done()
	if err != nil {
		return fmt.Errorf("ollama create %s: %w", name, err)
	}
	fmt.Printf("created %s\n", name)
	return nil
}

// --- rm / cp / show --------------------------------------------------------

func cmdRemove(client *api.Client, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: charly ollama rm <model> [<model> ...]")
	}
	for _, m := range args {
		if err := client.Delete(context.Background(), &api.DeleteRequest{Model: m}); err != nil {
			return fmt.Errorf("ollama rm %s: %w", m, err)
		}
		fmt.Printf("deleted %s\n", m)
	}
	return nil
}

func cmdCopy(client *api.Client, args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: charly ollama cp <src> <dst>")
	}
	if err := client.Copy(context.Background(), &api.CopyRequest{Source: args[0], Destination: args[1]}); err != nil {
		return fmt.Errorf("ollama cp %s %s: %w", args[0], args[1], err)
	}
	fmt.Printf("copied %s to %s\n", args[0], args[1])
	return nil
}

func cmdShow(client *api.Client, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: charly ollama show <model>")
	}
	resp, err := client.Show(context.Background(), &api.ShowRequest{Model: args[0]})
	if err != nil {
		return fmt.Errorf("ollama show %s: %w", args[0], err)
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	if !resp.ModifiedAt.IsZero() {
		fmt.Fprintf(w, "modified:\t%s\n", resp.ModifiedAt.Format(time.RFC3339))
	}
	if resp.Details.Format != "" {
		fmt.Fprintf(w, "format:\t%s\n", resp.Details.Format)
	}
	if resp.Details.Family != "" {
		fmt.Fprintf(w, "family:\t%s\n", resp.Details.Family)
	}
	if resp.Details.ParameterSize != "" {
		fmt.Fprintf(w, "parameters:\t%s\n", resp.Details.ParameterSize)
	}
	if resp.Details.QuantizationLevel != "" {
		fmt.Fprintf(w, "quantization:\t%s\n", resp.Details.QuantizationLevel)
	}
	for _, c := range resp.Capabilities {
		fmt.Fprintf(w, "capability:\t%s\n", c)
	}
	if err := w.Flush(); err != nil {
		return err
	}
	if resp.Modelfile != "" {
		fmt.Printf("\n# Modelfile\n%s\n", resp.Modelfile)
	}
	return nil
}

// --- run / stop / version --------------------------------------------------

func cmdRun(client *api.Client, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: charly ollama run <model> [prompt…]")
	}
	model := args[0]
	if len(args) > 1 {
		return generateOnce(client, model, strings.Join(args[1:], " "))
	}
	return chatRepl(client, model)
}

// generateOnce streams a single completion to stdout.
func generateOnce(client *api.Client, model, prompt string) error {
	req := &api.GenerateRequest{Model: model, Prompt: prompt}
	err := client.Generate(context.Background(), req, func(resp api.GenerateResponse) error {
		fmt.Print(resp.Response)
		if resp.Done {
			fmt.Println()
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("ollama run %s: %w", model, err)
	}
	return nil
}

// chatRepl is the interactive no-prompt form of `run`: a minimal read-line loop over stdin
// keeping chat history. `/bye` or EOF exits.
func chatRepl(client *api.Client, model string) error {
	fmt.Fprintf(os.Stderr, "chatting with %s — /bye or Ctrl-D to exit\n", model)
	var history []api.Message
	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Fprint(os.Stderr, ">>> ")
		if !scanner.Scan() {
			fmt.Fprintln(os.Stderr)
			return scanner.Err()
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if line == "/bye" {
			return nil
		}
		history = append(history, api.Message{Role: "user", Content: line})
		var reply strings.Builder
		req := &api.ChatRequest{Model: model, Messages: history}
		err := client.Chat(context.Background(), req, func(resp api.ChatResponse) error {
			fmt.Print(resp.Message.Content)
			reply.WriteString(resp.Message.Content)
			if resp.Done {
				fmt.Println()
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("ollama run %s: %w", model, err)
		}
		history = append(history, api.Message{Role: "assistant", Content: reply.String()})
	}
}

// cmdStop unloads a model: a zero-keepalive generate, the same mechanism the upstream CLI
// uses.
func cmdStop(client *api.Client, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: charly ollama stop <model>")
	}
	req := &api.GenerateRequest{Model: args[0], KeepAlive: &api.Duration{}}
	err := client.Generate(context.Background(), req, func(api.GenerateResponse) error { return nil })
	if err != nil {
		return fmt.Errorf("ollama stop %s: %w", args[0], err)
	}
	fmt.Printf("stopped %s\n", args[0])
	return nil
}

func cmdVersion(client *api.Client, args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("usage: charly ollama version")
	}
	v, err := client.Version(context.Background())
	if err != nil {
		return fmt.Errorf("ollama version: %w", err)
	}
	fmt.Printf("ollama server version is %s\n", v)
	return nil
}

// --- formatting helpers ----------------------------------------------------

// humanSize renders bytes as a compact IEC string (the ollama CLI prints SI-ish two-digit
// values; this keeps the same spirit with no dependency).
func humanSize(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit && exp < 5; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}

// humanTime renders a duration as one coarse unit: negative durations (past) render as
// "<unit> ago", positive (future) as "<unit> from now" — pass time.Until(t) for both the
// MODIFIED (list) and UNTIL (ps) columns.
func humanTime(d time.Duration) string {
	future := d > 0
	if !future {
		d = -d
	}
	var s string
	switch {
	case d < time.Minute:
		s = fmt.Sprintf("%d seconds", int(d.Seconds()))
	case d < time.Hour:
		s = fmt.Sprintf("%d minutes", int(d.Minutes()))
	case d < 24*time.Hour:
		s = fmt.Sprintf("%d hours", int(d.Hours()))
	case d < 30*24*time.Hour:
		s = fmt.Sprintf("%d days", int(d.Hours()/24))
	case d < 365*24*time.Hour:
		s = fmt.Sprintf("%d months", int(d.Hours()/(24*30)))
	default:
		s = fmt.Sprintf("%d years", int(d.Hours()/(24*365)))
	}
	if future {
		return s + " from now"
	}
	return s + " ago"
}
