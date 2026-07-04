package secrets

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/alecthomas/kong"
	"golang.org/x/term"
	"gopkg.in/yaml.v3"

	"github.com/opencharly/sdk/vmshared"
)

// command.go is the command:secrets leg of this plugin — the externalized `charly
// secrets …` CLI, ported OUT of charly's core (the deleted charly/secrets_cmd.go +
// plugin_command_secrets.go) so the go-keyring credential store + the Secret Service
// client no longer link into the core binary (the C2 dep-shed). It is the COMMAND-class
// companion of this same module's verb:credential store-backend verb.
//
// Dispatch contract (charly/provider_command_external.go dispatchExternalCommand): on
// `charly secrets <args…>`, charly RESOLVES this plugin's binary and syscall.Exec's it
// with the pass-through tokens after the `secrets` word, in CLI mode (the go-plugin
// handshake cookie is stripped, so sdk.Main runs cliMain instead of serving gRPC). The
// plugin therefore owns real terminal stdio/TTY — secure password prompts
// (term.ReadPassword), $EDITOR for `secrets gpg edit`, and live `gpg` shell-outs all
// reach the real terminal.

// cliMain is the CLI-mode entry point (sdk.Main calls it when charly syscall.Exec'd this
// plugin as a command passthrough). It parses the pass-through tokens against
// SecretsCmdGroup and dispatches the selected command via kctx.Run(). Returns the process
// exit code.
func cliMain(args []string) int {
	// Arm the temp-file kill-survivability signal handler so a Ctrl-C during `secrets gpg
	// edit` / `decrypt` (which stage a plaintext temp) still reaps the RegisterTempCleanup'd
	// file (the graceful-exit defer covers normal exit; this covers SIGINT/SIGTERM/SIGHUP).
	vmshared.InstallSignalHandler()
	var grp SecretsCmdGroup
	parser, err := kong.New(&grp,
		kong.Name("secrets"),
		kong.Description("charly secrets — credential store (Secret Service / config) + GPG-encrypted .secrets files"),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "plugin-secrets: build kong parser: %v\n", err)
		return 1
	}
	kctx, err := parser.Parse(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "plugin-secrets: parse `charly secrets %v`: %v\n", args, err)
		return 1
	}
	if err := kctx.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "charly secrets: %v\n", err)
		return 1
	}
	return 0
}

// SecretsCmdGroup groups `charly secrets` subcommands. The plain (non-gpg)
// subcommands operate on the active credential store resolved by
// DefaultCredentialStore() — the system keyring (Secret Service, incl.
// KeePassXC via FdoSecrets) with the config-file plaintext fallback for
// headless hosts. The `gpg` subgroup manages GPG-encrypted .secrets files
// and is independent of the credential store.
type SecretsCmdGroup struct {
	Delete         SecretsDeleteCmd        `cmd:"" help:"Delete a credential from the active store"`
	Export         SecretsExportCmd        `cmd:"" help:"Export all charly credentials to stdout (plaintext!)"`
	Get            SecretsGetCmd           `cmd:"" help:"Get a credential value"`
	Gpg            SecretsGpgCmd           `cmd:"" help:"Manage GPG-encrypted .secrets environment files"`
	Import         SecretsImportCmd        `cmd:"" help:"Import plaintext config + keyring credentials into the active store"`
	List           SecretsListCmd          `cmd:"" help:"List all charly credentials in the active store"`
	MigrateSecrets ConfigMigrateSecretsCmd `cmd:"migrate-secrets" help:"Migrate plaintext credentials from config.yml to system keyring"`
	Set            SecretsSetCmd           `cmd:"" help:"Set a credential"`
}

// --- List ---

// SecretsListCmd lists credentials known to the active store.
type SecretsListCmd struct {
	Service string `arg:"" optional:"" help:"Service prefix filter (e.g., charly/vnc)"`
}

func (c *SecretsListCmd) Run() error {
	names, err := collectCredentialNames()
	if err != nil {
		return err
	}

	shown := 0
	for _, n := range names {
		full := n.Service + "/" + n.Key
		if c.Service != "" && !strings.HasPrefix(full, c.Service) {
			continue
		}
		fmt.Println(full)
		shown++
	}
	if shown == 0 {
		fmt.Fprintln(os.Stderr, "No entries found.")
	}
	return nil
}

// --- Get ---

// SecretsGetCmd gets a credential value from the active store.
type SecretsGetCmd struct {
	Service string `arg:"" help:"Service name (e.g., charly/vnc)"`
	Key     string `arg:"" help:"Entry key (e.g., my-box)"`
}

func (c *SecretsGetCmd) Run() error {
	store := DefaultCredentialStore()
	val, err := store.Get(c.Service, c.Key)
	if err != nil {
		return err
	}
	if val == "" {
		return fmt.Errorf("no entry found for %s/%s", c.Service, c.Key)
	}
	fmt.Println(val)
	return nil
}

// --- Set ---

// SecretsSetCmd sets a credential in the active store.
type SecretsSetCmd struct {
	Service  string `arg:"" help:"Service name (e.g., charly/vnc)"`
	Key      string `arg:"" help:"Entry key (e.g., my-box)"`
	Value    string `arg:"" optional:"" help:"Value to set (omit to prompt securely)"`
	Generate bool   `long:"generate" help:"Generate random value and print to stdout"`
}

func (c *SecretsSetCmd) Run() error {
	var value string
	switch {
	case c.Generate:
		b := make([]byte, 16)
		if _, err := rand.Read(b); err != nil {
			return fmt.Errorf("generating random value: %w", err)
		}
		value = hex.EncodeToString(b)
		fmt.Println(value)
	case c.Value != "":
		value = c.Value
	default:
		var err error
		value, err = promptPassword("Secret value: ")
		if err != nil {
			return err
		}
		if value == "" {
			return fmt.Errorf("value cannot be empty")
		}
	}

	PrintStoreInfo()
	store := DefaultCredentialStore()
	if err := store.Set(c.Service, c.Key, value); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Stored %s/%s in %s\n", c.Service, c.Key, store.Name())
	return nil
}

// --- Delete ---

// SecretsDeleteCmd deletes a credential from the active store.
type SecretsDeleteCmd struct {
	Service string `arg:"" help:"Service name (e.g., charly/vnc)"`
	Key     string `arg:"" help:"Entry key"`
}

func (c *SecretsDeleteCmd) Run() error {
	store := DefaultCredentialStore()
	if err := store.Delete(c.Service, c.Key); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Deleted %s/%s from %s\n", c.Service, c.Key, store.Name())
	return nil
}

// --- Import ---

// SecretsImportCmd consolidates credentials from config.yml plaintext and the
// keyring into the active store. This COPIES (does not clear the source) —
// distinct from `charly settings migrate-secrets`, which MOVES config plaintext
// into the keyring and then strips the plaintext copies.
type SecretsImportCmd struct {
	DryRun bool `long:"dry-run" help:"Show what would be imported without making changes"`
}

func (c *SecretsImportCmd) Run() error {
	cfg, err := LoadRuntimeConfig()
	if err != nil {
		return err
	}
	configEntries := PlaintextCredentialEntries(cfg)

	// Collect credentials from the keyring (if available) via the shadow index.
	var keyringEntries []struct{ Service, Key, Value string }
	keyringStore := &KeyringStore{}
	if keyringStore.Probe() == nil {
		for _, indexEntry := range cfg.KeyringKeys {
			lastSlash := strings.LastIndex(indexEntry, "/")
			if lastSlash < 0 {
				continue
			}
			service := indexEntry[:lastSlash]
			key := indexEntry[lastSlash+1:]
			val, err := keyringStore.Get(service, key)
			if err == nil && val != "" {
				keyringEntries = append(keyringEntries, struct{ Service, Key, Value string }{service, key, val})
			}
		}
	}

	total := len(configEntries) + len(keyringEntries)
	if total == 0 {
		fmt.Fprintln(os.Stderr, "No credentials found to import.")
		return nil
	}

	if c.DryRun {
		fmt.Fprintf(os.Stderr, "Found %d credential(s) to import:\n\n", total)
		for _, e := range configEntries {
			fmt.Fprintf(os.Stderr, "  %-45s (from config.yml)\n", e.Service+"/"+e.Key)
		}
		for _, e := range keyringEntries {
			fmt.Fprintf(os.Stderr, "  %-45s (from keyring)\n", e.Service+"/"+e.Key)
		}
		fmt.Fprintln(os.Stderr, "\nRun without --dry-run to import.")
		return nil
	}

	PrintStoreInfo()
	store := DefaultCredentialStore()

	imported := 0
	fmt.Fprintf(os.Stderr, "Importing %d credential(s):\n", total)
	for _, e := range configEntries {
		if err := store.Set(e.Service, e.Key, e.Value); err != nil {
			fmt.Fprintf(os.Stderr, "  %-45s → FAILED: %v\n", e.Service+"/"+e.Key, err)
			continue
		}
		fmt.Fprintf(os.Stderr, "  %-45s → %s ✓ (from config)\n", e.Service+"/"+e.Key, store.Name())
		imported++
	}
	for _, e := range keyringEntries {
		if err := store.Set(e.Service, e.Key, e.Value); err != nil {
			fmt.Fprintf(os.Stderr, "  %-45s → FAILED: %v\n", e.Service+"/"+e.Key, err)
			continue
		}
		fmt.Fprintf(os.Stderr, "  %-45s → %s ✓ (from keyring)\n", e.Service+"/"+e.Key, store.Name())
		imported++
	}

	fmt.Fprintf(os.Stderr, "\nImported %d credential(s) into %s\n", imported, store.Name())
	return nil
}

// --- Export ---

// SecretsExportCmd exports all known charly credentials, resolving each value
// through the active store (with config-file fallback).
type SecretsExportCmd struct {
	Format string `long:"format" default:"yaml" enum:"yaml,json" help:"Output format (yaml, json)"`
}

func (c *SecretsExportCmd) Run() error {
	fmt.Fprintln(os.Stderr, "WARNING: This exports plaintext credentials. Handle with care.")

	names, err := collectCredentialNames()
	if err != nil {
		return err
	}

	// Build nested map: service -> key -> value
	data := make(map[string]map[string]string)
	for _, n := range names {
		val, source := ResolveCredential("", n.Service, n.Key, "")
		if source == "locked" {
			fmt.Fprintf(os.Stderr, "  %s/%s — keyring locked, skipped\n", n.Service, n.Key)
			continue
		}
		if val == "" {
			continue
		}
		if data[n.Service] == nil {
			data[n.Service] = make(map[string]string)
		}
		data[n.Service][n.Key] = val
	}

	switch c.Format {
	case "json":
		out, err := json.MarshalIndent(data, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(out))
	default:
		out, err := yaml.Marshal(data)
		if err != nil {
			return err
		}
		fmt.Print(string(out))
	}
	return nil
}

// --- Helpers ---

// collectCredentialNames returns the union of credential identities known to
// the active store: the keyring shadow index (cfg.KeyringKeys) plus the
// plaintext config-file entries. Values are NOT included — callers resolve
// them through the store / ResolveCredential so the active backend supplies
// the current value. Results are de-duplicated by "service/key".
func collectCredentialNames() ([]struct{ Service, Key string }, error) {
	cfg, err := LoadRuntimeConfig()
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []struct{ Service, Key string }
	add := func(service, key string) {
		id := service + "/" + key
		if seen[id] {
			return
		}
		seen[id] = true
		out = append(out, struct{ Service, Key string }{service, key})
	}
	for _, entry := range cfg.KeyringKeys {
		lastSlash := strings.LastIndex(entry, "/")
		if lastSlash < 0 {
			continue
		}
		add(entry[:lastSlash], entry[lastSlash+1:])
	}
	for _, e := range PlaintextCredentialEntries(cfg) {
		add(e.Service, e.Key)
	}
	return out, nil
}

// promptPassword reads a password from the terminal without echo.
func promptPassword(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	pw, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", fmt.Errorf("reading password: %w", err)
	}
	return string(pw), nil
}
