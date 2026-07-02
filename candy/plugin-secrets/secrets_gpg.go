package main

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	dbus "github.com/godbus/dbus/v5"
	"golang.org/x/term"
)

// SecretsGpgCmd groups subcommands for managing GPG-encrypted .secrets files.
// These are project-level env files (KEY=VALUE), encrypted with GPG.
type SecretsGpgCmd struct {
	AddRecipient SecretsGpgAddRecipientCmd `cmd:"add-recipient" help:"Re-encrypt .secrets with an additional GPG recipient"`
	Decrypt      SecretsGpgDecryptCmd      `cmd:"" help:"Decrypt .secrets to a plaintext file"`
	Doctor       SecretsGpgDoctorCmd       `cmd:"" help:"Show GPG agent, keys, Secret Service, and .secrets health"`
	Edit         SecretsGpgEditCmd         `cmd:"" help:"Decrypt, edit in $EDITOR, re-encrypt"`
	Encrypt      SecretsGpgEncryptCmd      `cmd:"" help:"Encrypt a plaintext env file to .secrets"`
	Env          SecretsGpgEnvCmd          `cmd:"" help:"Export decrypted .secrets as shell export statements"`
	ExportKey    SecretsGpgExportKeyCmd    `cmd:"export-key" help:"Export GPG key(s) to directory and/or Secret Service"`
	ImportKey    SecretsGpgImportKeyCmd    `cmd:"import-key" help:"Import GPG key(s) from file, directory, or Secret Service"`
	Recipients   SecretsGpgRecipientsCmd   `cmd:"" help:"List GPG recipients of .secrets file"`
	Set          SecretsGpgSetCmd          `cmd:"" help:"Set a single KEY=VALUE in .secrets"`
	Setup        SecretsGpgSetupCmd        `cmd:"" help:"Configure gpg-agent, import/generate key, store passphrase in Secret Service"`
	Show         SecretsGpgShowCmd         `cmd:"" help:"Decrypt and print .secrets to stdout"`
	Unset        SecretsGpgUnsetCmd        `cmd:"" help:"Remove a key from .secrets"`
}

// --- show ---

type SecretsGpgShowCmd struct {
	File string `short:"f" long:"file" default:".secrets" help:"Path to encrypted file"`
}

func (c *SecretsGpgShowCmd) Run() error {
	if err := requireGpg(); err != nil {
		return err
	}
	plaintext, err := gpgDecryptToBytes(c.File)
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(plaintext)
	return err
}

// --- env ---

type SecretsGpgEnvCmd struct {
	File string `short:"f" long:"file" default:".secrets" help:"Path to encrypted file"`
}

func (c *SecretsGpgEnvCmd) Run() error {
	if err := requireGpg(); err != nil {
		return err
	}

	// Silent skip if file doesn't exist (matches dotenv_gpg_if_exists behavior)
	if _, err := os.Stat(c.File); os.IsNotExist(err) {
		return nil
	}

	plaintext, err := gpgDecryptToBytes(c.File)
	if err != nil {
		return err
	}

	entries, err := ParseEnvBytes(plaintext)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		before, after, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		key := before
		value := after
		fmt.Printf("export %s=%s\n", key, shellQuote(value))
	}

	return nil
}

// --- edit ---

type SecretsGpgEditCmd struct {
	File string `short:"f" long:"file" default:".secrets" help:"Path to encrypted file"`
}

func (c *SecretsGpgEditCmd) Run() error {
	if err := requireGpg(); err != nil {
		return err
	}

	// Decrypt to temp file
	tmp, err := os.CreateTemp("", "charly-secrets-*.env")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := tmp.Name()
	RegisterTempCleanup(tmpPath)
	defer func() { secureDelete(tmpPath); UnregisterTempCleanup(tmpPath) }()

	plaintext, decErr := gpgDecryptToBytes(c.File)
	if decErr != nil {
		_ = tmp.Close()
		return decErr
	}
	if _, writeErr := tmp.Write(plaintext); writeErr != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing temp file: %w", writeErr)
	}
	_ = tmp.Close()

	// Get recipients before editing
	recipients, err := listRecipients(c.File)
	if err != nil {
		return fmt.Errorf("listing recipients: %w", err)
	}
	if len(recipients) == 0 {
		return fmt.Errorf("could not determine recipients for %s", c.File)
	}

	// Get file stat for change detection
	infoBefore, _ := os.Stat(tmpPath)

	// Open in editor
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}
	editorCmd := exec.Command(editor, tmpPath)
	editorCmd.Stdin = os.Stdin
	editorCmd.Stdout = os.Stdout
	editorCmd.Stderr = os.Stderr
	if err := editorCmd.Run(); err != nil {
		return fmt.Errorf("editor failed: %w", err)
	}

	// Check if file was modified
	infoAfter, _ := os.Stat(tmpPath)
	if infoBefore != nil && infoAfter != nil && infoBefore.ModTime().Equal(infoAfter.ModTime()) {
		fmt.Fprintln(os.Stderr, "No changes made.")
		return nil
	}

	// Re-encrypt with same recipients
	return gpgEncryptFile(tmpPath, c.File, recipients)
}

// --- encrypt ---

type SecretsGpgEncryptCmd struct {
	Input     string   `short:"i" long:"input" default:".env" help:"Input plaintext file"`
	Output    string   `short:"o" long:"output" default:".secrets" help:"Output encrypted file"`
	Recipient []string `short:"r" long:"recipient" help:"GPG recipient key ID (repeatable)"`
}

func (c *SecretsGpgEncryptCmd) Run() error {
	if err := requireGpg(); err != nil {
		return err
	}
	if len(c.Recipient) == 0 {
		return fmt.Errorf("at least one --recipient (-r) is required")
	}
	return gpgEncryptFile(c.Input, c.Output, c.Recipient)
}

// --- decrypt ---

type SecretsGpgDecryptCmd struct {
	Input  string `short:"i" long:"input" default:".secrets" help:"Input encrypted file"`
	Output string `short:"o" long:"output" help:"Output file (default: stdout)"`
}

func (c *SecretsGpgDecryptCmd) Run() error {
	if err := requireGpg(); err != nil {
		return err
	}
	plaintext, err := gpgDecryptToBytes(c.Input)
	if err != nil {
		return err
	}
	if c.Output != "" {
		return os.WriteFile(c.Output, plaintext, 0600)
	}
	_, err = os.Stdout.Write(plaintext)
	return err
}

// --- set ---

type SecretsGpgSetCmd struct {
	Key       string   `arg:"" help:"Environment variable name"`
	Value     string   `arg:"" help:"Value to set"`
	File      string   `short:"f" long:"file" default:".secrets" help:"Path to encrypted file"`
	Recipient []string `short:"r" long:"recipient" help:"GPG recipient (required if creating new file)"`
}

func (c *SecretsGpgSetCmd) Run() error {
	if err := requireGpg(); err != nil {
		return err
	}
	return modifySecrets(c.File, c.Recipient, func(lines []string) []string {
		return upsertEnvLine(lines, c.Key, c.Value)
	})
}

// --- unset ---

type SecretsGpgUnsetCmd struct {
	Key  string `arg:"" help:"Environment variable name to remove"`
	File string `short:"f" long:"file" default:".secrets" help:"Path to encrypted file"`
}

func (c *SecretsGpgUnsetCmd) Run() error {
	if err := requireGpg(); err != nil {
		return err
	}
	return modifySecrets(c.File, nil, func(lines []string) []string {
		return removeEnvLine(lines, c.Key)
	})
}

// --- add-recipient ---

type SecretsGpgAddRecipientCmd struct {
	KeyID string `arg:"" help:"GPG key ID of the new recipient"`
	File  string `short:"f" long:"file" default:".secrets" help:"Path to encrypted file"`
}

func (c *SecretsGpgAddRecipientCmd) Run() error {
	if err := requireGpg(); err != nil {
		return err
	}

	recipients, err := listRecipients(c.File)
	if err != nil {
		return err
	}

	// Check if already a recipient
	if slices.Contains(recipients, c.KeyID) {
		fmt.Fprintf(os.Stderr, "Key %s is already a recipient.\n", c.KeyID)
		return nil
	}

	recipients = append(recipients, c.KeyID)

	// Decrypt and re-encrypt with new recipient list
	plaintext, err := gpgDecryptToBytes(c.File)
	if err != nil {
		return err
	}
	return gpgEncryptBytes(plaintext, c.File, recipients)
}

// --- recipients ---

type SecretsGpgRecipientsCmd struct {
	File string `short:"f" long:"file" default:".secrets" help:"Path to encrypted file"`
}

func (c *SecretsGpgRecipientsCmd) Run() error {
	if err := requireGpg(); err != nil {
		return err
	}
	recipients, err := listRecipients(c.File)
	if err != nil {
		return err
	}
	for _, r := range recipients {
		fmt.Println(r)
	}
	return nil
}

// --- helpers ---

func requireGpg() error {
	if _, err := exec.LookPath("gpg"); err != nil {
		return fmt.Errorf("gpg not found in PATH (install gnupg2)")
	}
	return nil
}

// gpgEncryptFile encrypts inputPath to outputPath for the given recipients.
//
// `--batch --trust-model always` lets gpg run without /dev/tty when the
// recipient key trust is unknown (e.g. a key imported from a backup that
// hasn't been ultimately trusted yet). Without these flags, gpg prompts
// "There is no assurance this key belongs to the named user. Use this
// key anyway?" and aborts when stdin is not a TTY. `--yes` answers the
// other prompts (overwrite, etc.); `--trust-model always` answers the
// trust prompt without requiring an explicit `--batch` opt-in.
func gpgEncryptFile(inputPath, outputPath string, recipients []string) error {
	args := make([]string, 0, 8+2*len(recipients)+1)
	args = append(args, "--batch", "--trust-model", "always", "--encrypt", "--armor", "--yes", "--output", outputPath)
	for _, r := range recipients {
		args = append(args, "-r", r)
	}
	args = append(args, inputPath)
	cmd := exec.Command("gpg", args...)
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// gpgDecryptToBytes decrypts a file and returns the plaintext.
// On failure, prints actionable diagnostics instead of raw GPG errors.
func gpgDecryptToBytes(path string) ([]byte, error) {
	var stderr bytes.Buffer
	cmd := exec.Command("gpg", "--quiet", "--batch", "--decrypt", path)
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		diagnoseGPGDecryptionFailure(path, stderr.String())
		return nil, fmt.Errorf("decrypting %s failed (see diagnostics above)", path)
	}
	return out, nil
}

// gpgEncryptBytes encrypts plaintext bytes to the given file for the recipients.
// See gpgEncryptFile for the rationale on `--batch --trust-model always`.
func gpgEncryptBytes(plaintext []byte, outputPath string, recipients []string) error {
	args := make([]string, 0, 8+2*len(recipients))
	args = append(args, "--batch", "--trust-model", "always", "--encrypt", "--armor", "--yes", "--output", outputPath)
	for _, r := range recipients {
		args = append(args, "-r", r)
	}
	cmd := exec.Command("gpg", args...)
	cmd.Stdin = strings.NewReader(string(plaintext))
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// listRecipients parses a GPG-encrypted file to extract recipient key IDs.
//
// Modern gpg 2.x's --list-packets attempts to decrypt the outer envelope to
// enumerate inner packets, which can fail when the secret key isn't unlocked
// (passphrase not cached, no pinentry display). However, the OUTER
// `:pubkey enc packet:` line that carries the recipient keyid is emitted to
// stdout BEFORE that decryption error. So we scan the captured output
// regardless of gpg's exit status — if we extract any recipients, we return
// them as success; we only surface gpg's error when the parse yielded
// nothing at all.
func listRecipients(path string) ([]string, error) {
	cmd := exec.Command("gpg", "--list-packets", "--batch", path)
	out, err := cmd.CombinedOutput()

	var recipients []string
	// Match lines like ":pubkey enc packet: ... keyid 5EA2283B420DE2B3"
	re := regexp.MustCompile(`keyid\s+([0-9A-Fa-f]+)`)
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "pubkey enc packet") {
			if matches := re.FindStringSubmatch(line); len(matches) > 1 {
				recipients = append(recipients, matches[1])
			}
		}
	}

	if len(recipients) > 0 {
		return recipients, nil
	}
	if err != nil {
		return nil, fmt.Errorf("listing packets for %s: %w\n%s", path, err, string(out))
	}
	return nil, nil
}

// modifySecrets decrypts .secrets, applies a transform function, and re-encrypts.
func modifySecrets(path string, fallbackRecipients []string, transform func([]string) []string) error {
	var lines []string
	var recipients []string

	if _, err := os.Stat(path); err == nil {
		// Existing file: decrypt and get recipients
		plaintext, decErr := gpgDecryptToBytes(path)
		if decErr != nil {
			return decErr
		}
		lines = strings.Split(string(plaintext), "\n")

		var listErr error
		recipients, listErr = listRecipients(path)
		if listErr != nil {
			return listErr
		}
	} else {
		// New file
		recipients = fallbackRecipients
	}

	if len(recipients) == 0 {
		return fmt.Errorf("no recipients known; specify --recipient (-r) when creating a new file")
	}

	lines = transform(lines)
	content := strings.Join(lines, "\n")
	// Ensure trailing newline
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}

	return gpgEncryptBytes([]byte(content), path, recipients)
}

// upsertEnvLine adds or replaces KEY=VALUE in the lines.
func upsertEnvLine(lines []string, key, value string) []string {
	prefix := key + "="
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, prefix) || trimmed == key {
			lines[i] = key + "=" + value
			return lines
		}
	}
	// Append before any trailing empty lines
	insertIdx := len(lines)
	for insertIdx > 0 && strings.TrimSpace(lines[insertIdx-1]) == "" {
		insertIdx--
	}
	result := make([]string, 0, len(lines)+1)
	result = append(result, lines[:insertIdx]...)
	result = append(result, key+"="+value)
	result = append(result, lines[insertIdx:]...)
	return result
}

// removeEnvLine removes all lines matching KEY= from the lines.
func removeEnvLine(lines []string, key string) []string {
	prefix := key + "="
	var result []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, prefix) || trimmed == key {
			continue
		}
		result = append(result, line)
	}
	return result
}

// secureDelete overwrites a file with zeros before deleting.
func secureDelete(path string) {
	if info, err := os.Stat(path); err == nil {
		zeros := make([]byte, info.Size())
		if werr := os.WriteFile(path, zeros, 0600); werr != nil {
			fmt.Fprintf(os.Stderr, "secureDelete: overwrite %s: %v\n", path, werr)
		}
	}
	_ = os.Remove(path)
}

// ── GPG/Secret Service helpers ──────────────────────────────────────

// gpgSecretKeyInfo holds parsed info about a GPG secret key.
type gpgSecretKeyInfo struct {
	KeyID       string // long key ID (e.g., "5EA2283B420DE2B3")
	Fingerprint string // full fingerprint
	UID         string // user ID (e.g., "Name <email>")
	Algorithm   string // e.g., "rsa4096"
	Expires     string // expiry date or empty
	Keygrips    []string
}

// getSecretKeys returns info about all GPG secret keys.
func getSecretKeys() []gpgSecretKeyInfo {
	cmd := exec.Command("gpg", "--list-secret-keys", "--keyid-format", "long", "--with-keygrip", "--with-colons")
	out, err := cmd.Output()
	if err != nil {
		return nil // no keys is not an error
	}
	var keys []gpgSecretKeyInfo
	var current *gpgSecretKeyInfo
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Split(line, ":")
		if len(fields) < 2 {
			continue
		}
		switch fields[0] {
		case "sec":
			if current != nil {
				keys = append(keys, *current)
			}
			current = &gpgSecretKeyInfo{}
			if len(fields) > 4 {
				current.KeyID = fields[4]
			}
			if len(fields) > 3 {
				current.Algorithm = fields[3]
			}
			if len(fields) > 6 && fields[6] != "" {
				current.Expires = fields[6]
			}
		case "uid":
			if current != nil && len(fields) > 9 {
				current.UID = fields[9]
			}
		case "fpr":
			if current != nil && current.Fingerprint == "" && len(fields) > 9 {
				current.Fingerprint = fields[9]
			}
		case "grp":
			if current != nil && len(fields) > 9 {
				current.Keygrips = append(current.Keygrips, fields[9])
			}
		}
	}
	if current != nil {
		keys = append(keys, *current)
	}
	return keys
}

// getKeygrip returns the primary keygrip for a given key ID.
func getKeygrip(keyID string) (string, error) {
	cmd := exec.Command("gpg", "--list-keys", "--with-keygrip", "--with-colons", keyID)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("getting keygrip for %s: %w", keyID, err)
	}
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		fields := strings.Split(scanner.Text(), ":")
		if len(fields) > 9 && fields[0] == "grp" {
			return fields[9], nil
		}
	}
	return "", fmt.Errorf("no keygrip found for key %s", keyID)
}

// keyExistsInKeyring checks if a key ID exists in the local GPG keyring.
func keyExistsInKeyring(keyID string) bool {
	cmd := exec.Command("gpg", "--list-keys", keyID)
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run() == nil
}

// checkAgentRunning returns true if gpg-agent is reachable.
func checkAgentRunning() bool {
	cmd := exec.Command("gpg-connect-agent", "/bye")
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run() == nil
}

// checkSecretServiceAvailable returns true if a usable target collection
// exists. Beyond the D-Bus name probe, it also calls resolveTargetCollection
// so it surfaces routing problems (no collections, default alias broken, all
// collections locked) BEFORE downstream code attempts a write.
func checkSecretServiceAvailable() bool {
	c, err := newSSClient()
	if err != nil {
		return false
	}
	defer c.close()
	if _, _, err := resolveTargetCollection(c, gpgPreferredCollectionLabel()); err != nil {
		return false
	}
	return true
}

// gpgPreferredCollectionLabel returns the user-pinned Secret Service
// collection label. Reuses the existing keyring_collection_label setting so a
// single pin governs both the credential store and the GPG keystore.
func gpgPreferredCollectionLabel() string {
	cfg, err := LoadRuntimeConfig()
	if err != nil {
		return ""
	}
	return cfg.KeyringCollectionLabel
}

// ssGpgEntry is a Secret Service item discovered via ssGpgSearch.
type ssGpgEntry struct {
	Path  dbus.ObjectPath
	Attrs map[string]string
	Label string
}

// ssGpgLookup performs an iteration-capable read across every healthy
// unlocked Secret Service collection, returning the secret value of the FIRST
// match. Replaces the old default-alias-only `secret-tool lookup` shell-out:
// entries stored in any collection (not just the one aliased as `default`)
// are now reachable.
func ssGpgLookup(attrs map[string]string) (string, error) {
	c, err := newSSClient()
	if err != nil {
		return "", fmt.Errorf("opening Secret Service: %w", err)
	}
	defer c.close()
	item, _, err := c.findItemByAttrsAnyCollection(attrs, gpgPreferredCollectionLabel())
	if err != nil {
		return "", err
	}
	secret, err := c.getSecret(item)
	if err != nil {
		return "", err
	}
	return string(secret), nil
}

// ssGpgStore writes an item under the resolved target collection. Routing
// priority: `default` alias → keyring_collection_label → first healthy
// unlocked. Entries co-locate with reads, so a key written under a pin is
// readable by a subsequent ssGpgLookup with the same pin. Replaces the old
// `secret-tool store` shell-out which was hardcoded to whichever collection
// the `default` alias happened to point at.
//
// On success, prints a one-line stderr confirmation naming the target
// collection so the user can verify routing.
func ssGpgStore(label string, value string, attrs map[string]string) error {
	c, err := newSSClient()
	if err != nil {
		return fmt.Errorf("opening Secret Service: %w", err)
	}
	defer c.close()
	target, targetLabel, err := resolveTargetCollection(c, gpgPreferredCollectionLabel())
	if err != nil {
		return fmt.Errorf("resolving Secret Service target collection: %w", err)
	}
	if _, err := c.createItem(target, attrs, []byte(value), label, true); err != nil {
		return fmt.Errorf("creating item in collection %q (%s): %w", targetLabel, target, err)
	}
	fmt.Fprintf(os.Stderr, "    [routed to collection: %s]\n", targetLabel)
	return nil
}

// ssGpgSearch enumerates items matching attrs across every healthy unlocked
// collection. Returns one ssGpgEntry per match. Used by importFromKeystore
// (find all keys with schema=org.gnupg.Key) and the doctor (count backed-up
// keys).
func ssGpgSearch(attrs map[string]string) ([]ssGpgEntry, error) {
	c, err := newSSClient()
	if err != nil {
		return nil, fmt.Errorf("opening Secret Service: %w", err)
	}
	defer c.close()

	paths, err := c.collections()
	if err != nil {
		return nil, fmt.Errorf("listing collections: %w", err)
	}

	var entries []ssGpgEntry
	for _, p := range paths {
		if err := c.isCollectionHealthy(p); err != nil {
			fmt.Fprintf(os.Stderr, "charly: skipping broken collection %s: %v\n", p, err)
			continue
		}
		if err := c.unlock(p); err != nil {
			fmt.Fprintf(os.Stderr, "charly: skipping locked collection %s: %v\n", p, err)
			continue
		}
		items, err := c.searchItemsByAttrs(p, attrs)
		if err != nil {
			fmt.Fprintf(os.Stderr, "charly: search failed on %s: %v\n", p, err)
			continue
		}
		for _, item := range items {
			itemLabel, itemAttrs, mderr := c.itemMetadata(item)
			if mderr != nil {
				fmt.Fprintf(os.Stderr, "charly: cannot read metadata of %s: %v\n", item, mderr)
				continue
			}
			entries = append(entries, ssGpgEntry{
				Path:  item,
				Attrs: itemAttrs,
				Label: itemLabel,
			})
		}
	}
	return entries, nil
}

const (
	ssSchemaPassphrase = "org.gnupg.Passphrase"
	ssSchemaKey        = "org.gnupg.Key"
)

// gpgAgentConfContent returns the desired gpg-agent.conf content.
func gpgAgentConfContent() string {
	return `# gpg-agent.conf — managed by charly secrets gpg setup
# pinentry-qt has libsecret support: auto-retrieves passphrases from Secret Service (KeePassXC)
pinentry-program /usr/bin/pinentry-qt

# Cache passphrases for 8 hours (28800 seconds)
default-cache-ttl 28800

# Maximum cache lifetime: 12 hours
max-cache-ttl 43200

# Allow external tools to preset passphrases
allow-preset-passphrase

# Allow loopback pinentry mode (useful for scripts)
allow-loopback-pinentry
`
}

// installAgentConf installs gpg-agent.conf if missing or outdated.
func installAgentConf() (changed bool, err error) {
	home, _ := os.UserHomeDir()
	confPath := filepath.Join(home, ".gnupg", "gpg-agent.conf")
	desired := gpgAgentConfContent()

	existing, readErr := os.ReadFile(confPath)
	if readErr == nil && string(existing) == desired {
		return false, nil
	}

	// Back up existing file if different
	if readErr == nil {
		backup := confPath + ".bak"
		_ = os.WriteFile(backup, existing, 0600)
		fmt.Fprintf(os.Stderr, "  Backed up existing config to %s\n", backup)
	}

	// Ensure .gnupg directory exists
	gnupgDir := filepath.Dir(confPath)
	if err := os.MkdirAll(gnupgDir, 0700); err != nil {
		return false, fmt.Errorf("creating %s: %w", gnupgDir, err)
	}

	if err := os.WriteFile(confPath, []byte(desired), 0600); err != nil {
		return false, fmt.Errorf("writing %s: %w", confPath, err)
	}
	return true, nil
}

// enableSystemdSockets enables and starts gpg-agent systemd user sockets.
func enableSystemdSockets() error {
	for _, unit := range []string{"gpg-agent.socket", "gpg-agent-extra.socket"} {
		cmd := exec.Command("systemctl", "--user", "enable", "--now", unit)
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("enabling %s: %w", unit, err)
		}
	}
	return nil
}

// restartAgent kills and restarts gpg-agent via socket activation.
func restartAgent() error {
	kill := exec.Command("gpgconf", "--kill", "gpg-agent")
	_ = kill.Run()

	verify := exec.Command("gpg-connect-agent", "/bye")
	verify.Stderr = nil
	if err := verify.Run(); err != nil {
		return fmt.Errorf("gpg-agent failed to restart: %w", err)
	}
	return nil
}

// pinentryHasLibsecret checks if pinentry-qt links against libsecret.
func pinentryHasLibsecret() bool {
	path, err := exec.LookPath("pinentry-qt")
	if err != nil {
		return false
	}
	cmd := exec.Command("ldd", path)
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), "libsecret")
}

// formatGPGDate converts a GPG colon-format date (Unix timestamp) to human-readable.
func formatGPGDate(ts string) string {
	if unix, err := strconv.ParseInt(ts, 10, 64); err == nil {
		return time.Unix(unix, 0).Format("2006-01-02")
	}
	return ts // return as-is if not a timestamp
}

// gpgVersion returns the GPG version string.
func gpgVersion() string {
	cmd := exec.Command("gpg", "--version")
	out, _ := cmd.Output()
	if len(out) > 0 {
		line, _, _ := strings.Cut(string(out), "\n")
		return strings.TrimSpace(line)
	}
	return "unknown"
}

// autoSelectKeyID picks the only secret key, or returns empty if ambiguous.
func autoSelectKeyID(preferID string) (string, error) {
	if preferID != "" {
		return preferID, nil
	}
	keys := getSecretKeys()
	if len(keys) == 0 {
		return "", fmt.Errorf("no GPG secret keys found")
	}
	if len(keys) == 1 {
		return keys[0].KeyID, nil
	}
	fmt.Fprintln(os.Stderr, "Multiple GPG secret keys found:")
	for _, k := range keys {
		fmt.Fprintf(os.Stderr, "  %s  %s\n", k.KeyID, k.UID)
	}
	return "", fmt.Errorf("multiple keys found; specify --key-id")
}

// ── Diagnostics ─────────────────────────────────────────────────────

// diagnoseGPGDecryptionFailure prints actionable diagnostics when GPG decrypt fails.
func diagnoseGPGDecryptionFailure(path, gpgStderr string) {
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintf(os.Stderr, "charly: decrypting %s failed\n\n", path)

	// Extract recipient key IDs from the encrypted file
	recipients, _ := listRecipients(path)
	if len(recipients) > 0 {
		for _, r := range recipients {
			fmt.Fprintf(os.Stderr, "  File encrypted for:     %s\n", r)
			if keyExistsInKeyring(r) {
				fmt.Fprintf(os.Stderr, "  Key in local keyring:   yes\n")
			} else {
				fmt.Fprintf(os.Stderr, "  Key in local keyring:   NO ← likely cause\n")
			}
			// Check if key is backed up in Secret Service
			if val, _ := ssGpgLookup(map[string]string{
				"xdg:schema": ssSchemaKey,
				"keyid":      r,
			}); val != "" {
				fmt.Fprintf(os.Stderr, "  Key in Secret Service:  YES (restore: charly secrets gpg import-key --from-keystore)\n")
			}
		}
	} else {
		// Couldn't parse recipients, show raw GPG error
		for line := range strings.SplitSeq(strings.TrimSpace(gpgStderr), "\n") {
			if line != "" {
				fmt.Fprintf(os.Stderr, "  gpg: %s\n", line)
			}
		}
	}

	// Agent check
	if checkAgentRunning() {
		fmt.Fprintf(os.Stderr, "  gpg-agent:              running\n")
	} else {
		fmt.Fprintf(os.Stderr, "  gpg-agent:              NOT running\n")
	}

	// Config check
	home, _ := os.UserHomeDir()
	confPath := filepath.Join(home, ".gnupg", "gpg-agent.conf")
	if _, err := os.Stat(confPath); err == nil {
		fmt.Fprintf(os.Stderr, "  gpg-agent.conf:         present\n")
	} else {
		fmt.Fprintf(os.Stderr, "  gpg-agent.conf:         MISSING (run: charly secrets gpg setup)\n")
	}

	// Secret Service check
	if checkSecretServiceAvailable() {
		fmt.Fprintf(os.Stderr, "  Secret Service:         available\n")
	} else {
		fmt.Fprintf(os.Stderr, "  Secret Service:         NOT available\n")
	}

	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "To fix:")

	anyKeyMissing := false
	for _, r := range recipients {
		if !keyExistsInKeyring(r) {
			anyKeyMissing = true
			break
		}
	}
	if anyKeyMissing {
		fmt.Fprintln(os.Stderr, "  charly secrets gpg import-key <path-to-key>      # import from file/directory")
		fmt.Fprintln(os.Stderr, "  charly secrets gpg import-key --from-keystore     # restore from KeePassXC")
	}
	fmt.Fprintln(os.Stderr, "  charly secrets gpg setup                          # configure gpg-agent + Secret Service")
	fmt.Fprintln(os.Stderr, "  charly secrets gpg doctor                         # verify everything works")
	fmt.Fprintln(os.Stderr, "")
}

// ── import-key command ──────────────────────────────────────────────

type SecretsGpgImportKeyCmd struct {
	Path         string `arg:"" optional:"" help:"Path to key file or directory containing .asc/.gpg files"`
	FromKeystore bool   `long:"from-keystore" help:"Import from KeePassXC Secret Service"`
	KeyID        string `long:"key-id" help:"Specific key ID to import from keystore"`
	Passphrase   string `long:"passphrase" help:"GPG passphrase for secret key import (uses loopback pinentry)"`
}

func (c *SecretsGpgImportKeyCmd) Run() error {
	if err := requireGpg(); err != nil {
		return err
	}

	if c.FromKeystore {
		return c.importFromKeystore()
	}

	if c.Path == "" {
		return fmt.Errorf("path required (or use --from-keystore)")
	}

	info, err := os.Stat(c.Path)
	if err != nil {
		return fmt.Errorf("cannot access %s: %w", c.Path, err)
	}

	if info.IsDir() {
		return c.importFromDirectory(c.Path)
	}
	return c.importFile(c.Path)
}

func (c *SecretsGpgImportKeyCmd) importFromDirectory(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("reading directory %s: %w", dir, err)
	}

	imported := 0
	for _, e := range entries {
		name := e.Name()
		path := filepath.Join(dir, name)
		ext := strings.ToLower(filepath.Ext(name))

		if name == "ownertrust.txt" {
			fmt.Fprintf(os.Stderr, "  Importing ownertrust from %s\n", name)
			cmd := exec.Command("gpg", "--import-ownertrust", path)
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil {
				fmt.Fprintf(os.Stderr, "  Warning: ownertrust import failed: %v\n", err)
			} else {
				imported++
			}
			continue
		}

		if ext == ".asc" || ext == ".gpg" {
			fmt.Fprintf(os.Stderr, "  Importing key from %s\n", name)
			if err := c.importFile(path); err != nil {
				fmt.Fprintf(os.Stderr, "  Warning: import failed for %s: %v\n", name, err)
			} else {
				imported++
			}
		}
	}

	if imported == 0 {
		return fmt.Errorf("no key files (.asc, .gpg) or ownertrust.txt found in %s", dir)
	}

	// Print summary
	keys := getSecretKeys()
	if len(keys) > 0 {
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Imported keys:")
		for _, k := range keys {
			fmt.Fprintf(os.Stderr, "  %s  %s\n", k.KeyID, k.UID)
		}
	}
	return nil
}

func (c *SecretsGpgImportKeyCmd) importFile(path string) error {
	args := []string{"--import"}
	if c.Passphrase != "" {
		args = append(args, "--batch", "--pinentry-mode", "loopback", "--passphrase-fd", "0")
	}
	args = append(args, path)
	cmd := exec.Command("gpg", args...)
	if c.Passphrase != "" {
		cmd.Stdin = strings.NewReader(c.Passphrase)
	}
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (c *SecretsGpgImportKeyCmd) importFromKeystore() error {
	if !checkSecretServiceAvailable() {
		return fmt.Errorf("Secret Service not available on D-Bus (is KeePassXC running and unlocked?)")
	}

	// Iterate ALL healthy unlocked collections and gather every entry with
	// schema=org.gnupg.Key. Replaces the old search+lookup two-step where
	// search hit all collections but lookup only hit the default-aliased
	// one — the silent-skip bug for entries in non-default collections.
	entries, err := ssGpgSearch(map[string]string{"xdg:schema": ssSchemaKey})
	if err != nil {
		return fmt.Errorf("searching Secret Service: %w", err)
	}
	if len(entries) == 0 {
		return fmt.Errorf("no GPG keys found in Secret Service (schema: %s)", ssSchemaKey)
	}

	ssClient, err := newSSClient()
	if err != nil {
		return fmt.Errorf("opening Secret Service for getSecret: %w", err)
	}
	defer ssClient.close()

	imported := 0
	for _, entry := range entries {
		keyid := entry.Attrs["keyid"]
		if c.KeyID != "" && keyid != c.KeyID {
			continue
		}

		// Read the armored payload directly from the discovered item path —
		// no second search needed. The collection-iteration in ssGpgSearch
		// already proved the item is reachable.
		secret, err := ssClient.getSecret(entry.Path)
		if err != nil || len(secret) == 0 {
			fmt.Fprintf(os.Stderr, "  Warning: could not retrieve key %s from Secret Service: %v\n", keyid, err)
			continue
		}
		armoredKey := string(secret)

		// Import via stdin (key data on stdin, passphrase via --passphrase if provided)
		fmt.Fprintf(os.Stderr, "  Importing key %s from Secret Service\n", keyid)
		args := []string{"--import"}
		if c.Passphrase != "" {
			args = append(args, "--batch", "--pinentry-mode", "loopback", "--passphrase", c.Passphrase)
		}
		cmd := exec.Command("gpg", args...)
		cmd.Stdin = strings.NewReader(armoredKey)
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "  Warning: import failed for %s: %v\n", keyid, err)
			continue
		}
		imported++
	}

	if imported == 0 {
		if c.KeyID != "" {
			return fmt.Errorf("key %s not found in Secret Service", c.KeyID)
		}
		return fmt.Errorf("no keys could be imported from Secret Service")
	}

	keys := getSecretKeys()
	if len(keys) > 0 {
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Imported keys:")
		for _, k := range keys {
			fmt.Fprintf(os.Stderr, "  %s  %s\n", k.KeyID, k.UID)
		}
	}

	// Check if passphrases are already cached
	for _, k := range keys {
		for _, grip := range k.Keygrips {
			if val, _ := ssGpgLookup(map[string]string{
				"xdg:schema": ssSchemaPassphrase,
				"keygrip":    grip,
			}); val != "" {
				fmt.Fprintf(os.Stderr, "  Passphrase for %s already cached in Secret Service\n", k.KeyID)
				break
			}
		}
	}

	return nil
}

// ── export-key command ──────────────────────────────────────────────

type SecretsGpgExportKeyCmd struct {
	Path       string `arg:"" optional:"" help:"Output directory (writes public.asc, secret.asc, ownertrust.txt)"`
	ToKeystore bool   `long:"to-keystore" help:"Store key in KeePassXC Secret Service"`
	KeyID      string `long:"key-id" help:"GPG key ID to export (auto-selects if only one)"`
	Passphrase string `long:"passphrase" help:"Also store passphrase in Secret Service"`
}

func (c *SecretsGpgExportKeyCmd) Run() error {
	if err := requireGpg(); err != nil {
		return err
	}

	if c.Path == "" && !c.ToKeystore {
		return fmt.Errorf("specify output directory and/or --to-keystore")
	}

	keyID, err := autoSelectKeyID(c.KeyID)
	if err != nil {
		return err
	}

	if c.Path != "" {
		if err := c.exportToDirectory(keyID); err != nil {
			return err
		}
	}

	if c.ToKeystore {
		if err := c.exportToKeystore(keyID); err != nil {
			return err
		}
	}

	return nil
}

func (c *SecretsGpgExportKeyCmd) exportToDirectory(keyID string) error {
	if err := os.MkdirAll(c.Path, 0700); err != nil {
		return fmt.Errorf("creating directory %s: %w", c.Path, err)
	}

	// Export public key
	pubPath := filepath.Join(c.Path, "public.asc")
	pubCmd := exec.Command("gpg", "--armor", "--export", keyID)
	pubOut, err := pubCmd.Output()
	if err != nil {
		return fmt.Errorf("exporting public key: %w", err)
	}
	if err := os.WriteFile(pubPath, pubOut, 0644); err != nil {
		return fmt.Errorf("writing %s: %w", pubPath, err)
	}
	fmt.Fprintf(os.Stderr, "  Exported public key to %s\n", pubPath)

	// Export secret key
	secPath := filepath.Join(c.Path, "secret.asc")
	secCmd := exec.Command("gpg", "--armor", "--export-secret-keys", keyID)
	secOut, err := secCmd.Output()
	if err != nil {
		return fmt.Errorf("exporting secret key: %w", err)
	}
	if err := os.WriteFile(secPath, secOut, 0600); err != nil {
		return fmt.Errorf("writing %s: %w", secPath, err)
	}
	fmt.Fprintf(os.Stderr, "  Exported secret key to %s\n", secPath)

	// Export ownertrust
	trustPath := filepath.Join(c.Path, "ownertrust.txt")
	trustCmd := exec.Command("gpg", "--export-ownertrust")
	trustOut, err := trustCmd.Output()
	if err != nil {
		return fmt.Errorf("exporting ownertrust: %w", err)
	}
	if err := os.WriteFile(trustPath, trustOut, 0600); err != nil {
		return fmt.Errorf("writing %s: %w", trustPath, err)
	}
	fmt.Fprintf(os.Stderr, "  Exported ownertrust to %s\n", trustPath)

	return nil
}

func (c *SecretsGpgExportKeyCmd) exportToKeystore(keyID string) error {
	if !checkSecretServiceAvailable() {
		return fmt.Errorf("Secret Service not available on D-Bus (is KeePassXC running and unlocked?)")
	}

	// Get key info for label
	keys := getSecretKeys()
	uid := keyID
	for _, k := range keys {
		if k.KeyID == keyID {
			uid = k.UID
			break
		}
	}

	// Export armored secret key
	var secStderr bytes.Buffer
	cmd := exec.Command("gpg", "--armor", "--export-secret-keys", keyID)
	cmd.Stderr = &secStderr
	armoredKey, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("exporting secret key for keystore: %w\n%s", err, secStderr.String())
	}

	// Store in Secret Service. Routing goes through resolveTargetCollection
	// (default alias → keyring_collection_label → first healthy unlocked) so
	// the entry co-locates with the read path.
	label := fmt.Sprintf("GPG Key: %s (%s)", uid, keyID)
	if err := ssGpgStore(label, string(armoredKey), map[string]string{
		"xdg:schema": ssSchemaKey,
		"keyid":      keyID,
		"uid":        uid,
	}); err != nil {
		return fmt.Errorf("storing key in Secret Service: %w", err)
	}
	fmt.Fprintf(os.Stderr, "  Stored key %s in Secret Service\n", keyID)

	// Optionally store passphrase
	if c.Passphrase != "" {
		keygrip, err := getKeygrip(keyID)
		if err != nil {
			return err
		}
		passphraseLabel := fmt.Sprintf("GPG Passphrase: %s", keyID)
		if err := ssGpgStore(passphraseLabel, c.Passphrase, map[string]string{
			"xdg:schema": ssSchemaPassphrase,
			"keygrip":    keygrip,
		}); err != nil {
			return fmt.Errorf("storing passphrase in Secret Service: %w", err)
		}
		fmt.Fprintf(os.Stderr, "  Stored passphrase for keygrip %s in Secret Service\n", keygrip)
	}

	return nil
}

// ── setup command ───────────────────────────────────────────────────

type SecretsGpgSetupCmd struct {
	Import           string `long:"import" help:"Path to key file/directory to import before setup"`
	FromKeystore     bool   `long:"from-keystore" help:"Import key from KeePassXC Secret Service"`
	Passphrase       string `long:"passphrase" help:"GPG passphrase value (visible in shell history — prefer --prompt-passphrase)"`
	PromptPassphrase bool   `long:"prompt-passphrase" short:"p" help:"Prompt for passphrase securely (hidden input)"`
	KeyID            string `long:"key-id" help:"Use specific existing key"`
	SkipSS           bool   `long:"skip-secret-service" help:"Skip Secret Service passphrase storage"`
}

func (c *SecretsGpgSetupCmd) Run() error {
	fmt.Fprintln(os.Stderr, "charly secrets gpg setup")
	fmt.Fprintln(os.Stderr, "")

	// Step 1: Prerequisites
	fmt.Fprintln(os.Stderr, "Checking prerequisites:")
	if err := c.checkPrereqs(); err != nil {
		return err
	}

	// Step 2: gpg-agent.conf
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Configuring gpg-agent:")
	changed, err := installAgentConf()
	if err != nil {
		return fmt.Errorf("installing gpg-agent.conf: %w", err)
	}
	if changed {
		fmt.Fprintln(os.Stderr, "  gpg-agent.conf installed (pinentry-qt, 8h cache)")
	} else {
		fmt.Fprintln(os.Stderr, "  gpg-agent.conf already up to date")
	}

	// Step 3: Systemd sockets
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Enabling systemd sockets:")
	if err := enableSystemdSockets(); err != nil {
		fmt.Fprintf(os.Stderr, "  Warning: %v (non-systemd environment?)\n", err)
	} else {
		fmt.Fprintln(os.Stderr, "  gpg-agent.socket and gpg-agent-extra.socket enabled")
	}

	// Step 4: Restart agent
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Restarting gpg-agent:")
	if err := restartAgent(); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "  gpg-agent running")

	// Step 5: Key management
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "GPG key:")
	if err := c.ensureKey(); err != nil {
		return err
	}

	// Step 6: Secret Service passphrase
	if !c.SkipSS {
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Secret Service passphrase:")
		if err := c.ensurePassphrase(); err != nil {
			return err
		}
	}

	// Step 7: Verify
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Verification:")
	if err := c.verify(); err != nil {
		fmt.Fprintf(os.Stderr, "  Encrypt/decrypt test: FAILED (%v)\n", err)
		fmt.Fprintln(os.Stderr, "  The passphrase may need to be entered on first use (pinentry-qt will auto-cache it)")
	} else {
		fmt.Fprintln(os.Stderr, "  Encrypt/decrypt test: OK")
	}

	// Summary
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Setup complete. Run 'charly secrets gpg doctor' to verify full chain.")
	return nil
}

func (c *SecretsGpgSetupCmd) checkPrereqs() error {
	// Required for GPG itself.
	required := []struct{ name, cmdName string }{
		{"gpg", "gpg"},
		{"gpg-connect-agent", "gpg-connect-agent"},
	}
	// Optional — needed ONLY for KeePassXC Secret Service passphrase
	// auto-retricheck (pinentry-qt links libsecret to look the GPG passphrase up
	// in KeePassXC). Without them gpg still works: gpg-agent simply prompts via
	// the configured GUI/TTY pinentry instead of auto-retrieving. charly's own
	// credential store does NOT use these at all — it speaks the Secret Service
	// over D-Bus via the pure-Go go-keyring client. So a missing one is a note,
	// never a setup failure (the old "install libsecret" hard-error was
	// misleading — charly does not depend on libsecret).
	optional := []struct{ name, cmdName, note string }{
		{"pinentry-qt", "pinentry-qt", "GUI prompt + KeePassXC passphrase auto-retricheck"},
		{"secret-tool", "secret-tool", "from libsecret; KeePassXC passphrase auto-retricheck"},
	}
	allOK := true
	for _, ch := range required {
		if _, err := exec.LookPath(ch.cmdName); err != nil {
			fmt.Fprintf(os.Stderr, "  %s: MISSING (required)\n", ch.name)
			allOK = false
		} else {
			fmt.Fprintf(os.Stderr, "  %s: found\n", ch.name)
		}
	}
	for _, ch := range optional {
		if _, err := exec.LookPath(ch.cmdName); err != nil {
			fmt.Fprintf(os.Stderr, "  %s: not installed (optional — %s)\n", ch.name, ch.note)
		} else {
			fmt.Fprintf(os.Stderr, "  %s: found\n", ch.name)
		}
	}

	// pinentry-qt libsecret linkage — an optional convenience, never required.
	if pinentryHasLibsecret() {
		fmt.Fprintln(os.Stderr, "  pinentry-qt libsecret: yes (KeePassXC passphrase auto-retricheck available)")
	} else {
		fmt.Fprintln(os.Stderr, "  pinentry-qt libsecret: no (optional — gpg prompts interactively instead of auto-retrieving from KeePassXC)")
	}

	if !allOK {
		return fmt.Errorf("missing required prerequisites (see above)")
	}
	return nil
}

func (c *SecretsGpgSetupCmd) ensureKey() error {
	// Import if requested
	if c.Import != "" {
		importCmd := &SecretsGpgImportKeyCmd{Path: c.Import, FromKeystore: c.FromKeystore, KeyID: c.KeyID, Passphrase: c.Passphrase}
		if err := importCmd.Run(); err != nil {
			return err
		}
	} else if c.FromKeystore {
		importCmd := &SecretsGpgImportKeyCmd{FromKeystore: true, KeyID: c.KeyID, Passphrase: c.Passphrase}
		if err := importCmd.Run(); err != nil {
			return err
		}
	}

	// Check if we have keys now
	keys := getSecretKeys()
	if len(keys) > 0 {
		for _, k := range keys {
			fmt.Fprintf(os.Stderr, "  Found: %s  %s\n", k.KeyID, k.UID)
		}
		return nil
	}

	// No keys — generate one
	fmt.Fprintln(os.Stderr, "  No GPG secret keys found.")

	if c.Passphrase != "" || c.PromptPassphrase {
		return c.generateKey()
	}

	// Interactive prompt
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return fmt.Errorf("no GPG keys found; provide --import <path>, --from-keystore, or --passphrase for batch generation")
	}

	fmt.Fprint(os.Stderr, "  Generate a new GPG key? [Y/n] ")
	var answer string
	_, _ = fmt.Scanln(&answer) // best-effort: empty input (bare Enter) errors but correctly defaults to yes
	if answer != "" && answer[0] != 'Y' && answer[0] != 'y' {
		return fmt.Errorf("no GPG key available; run 'gpg --full-generate-key' manually")
	}

	return c.generateKey()
}

func (c *SecretsGpgSetupCmd) generateKey() error {
	// Get name/email from git config
	nameCmd := exec.Command("git", "config", "--global", "user.name")
	nameOut, _ := nameCmd.Output()
	emailCmd := exec.Command("git", "config", "--global", "user.email")
	emailOut, _ := emailCmd.Output()

	name := strings.TrimSpace(string(nameOut))
	email := strings.TrimSpace(string(emailOut))
	if name == "" || email == "" {
		return fmt.Errorf("git config user.name and user.email required for key generation")
	}

	passphrase := c.Passphrase
	if passphrase == "" {
		fmt.Fprint(os.Stderr, "  Enter passphrase for new key: ")
		pw, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr, "")
		if err != nil {
			return fmt.Errorf("reading passphrase: %w", err)
		}
		fmt.Fprint(os.Stderr, "  Confirm passphrase: ")
		pw2, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr, "")
		if err != nil {
			return fmt.Errorf("reading confirmation: %w", err)
		}
		if string(pw) != string(pw2) {
			return fmt.Errorf("passphrases do not match")
		}
		passphrase = string(pw)
	}

	batchConfig := fmt.Sprintf(`Key-Type: RSA
Key-Length: 4096
Subkey-Type: RSA
Subkey-Length: 4096
Name-Real: %s
Name-Email: %s
Expire-Date: 2y
Passphrase: %s
%%commit
`, name, email, passphrase)

	fmt.Fprintf(os.Stderr, "  Generating RSA-4096 key for %s <%s>...\n", name, email)
	cmd := exec.Command("gpg", "--batch", "--gen-key")
	cmd.Stdin = strings.NewReader(batchConfig)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("key generation failed: %w", err)
	}

	keys := getSecretKeys()
	for _, k := range keys {
		fmt.Fprintf(os.Stderr, "  Generated: %s  %s\n", k.KeyID, k.UID)
	}
	return nil
}

func (c *SecretsGpgSetupCmd) ensurePassphrase() error {
	if !checkSecretServiceAvailable() {
		fmt.Fprintln(os.Stderr, "  Secret Service not available — pinentry-qt will prompt on first use")
		return nil
	}

	keyID, err := autoSelectKeyID(c.KeyID)
	if err != nil {
		return err
	}

	keygrip, err := getKeygrip(keyID)
	if err != nil {
		return err
	}

	// Get ALL keygrips for the key (primary + subkeys share the same passphrase)
	keys := getSecretKeys()
	var keygrips []string
	for _, k := range keys {
		if k.KeyID == keyID {
			keygrips = k.Keygrips
			break
		}
	}
	if len(keygrips) == 0 {
		keygrips = []string{keygrip}
	}

	// Check if already stored for all keygrips
	allStored := true
	for _, grip := range keygrips {
		if val, _ := ssGpgLookup(map[string]string{
			"xdg:schema": ssSchemaPassphrase,
			"keygrip":    grip,
		}); val == "" {
			allStored = false
			break
		}
	}
	if allStored {
		fmt.Fprintf(os.Stderr, "  Passphrase already stored for all %d keygrip(s)\n", len(keygrips))
		return nil
	}

	passphrase := c.Passphrase
	forcePrompt := c.PromptPassphrase

	// If --prompt-passphrase, always prompt (allows correcting a bad stored passphrase)
	if forcePrompt && passphrase == "" {
		if !term.IsTerminal(int(os.Stdin.Fd())) {
			return fmt.Errorf("--prompt-passphrase requires an interactive terminal")
		}
		fmt.Fprint(os.Stderr, "  Enter GPG passphrase: ")
		pw, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr, "")
		if err != nil {
			return fmt.Errorf("reading passphrase: %w", err)
		}
		passphrase = string(pw)
		if passphrase == "" {
			return fmt.Errorf("passphrase cannot be empty")
		}
	}

	// If no passphrase provided, try to reuse from an already-stored keygrip
	// (all keygrips share the same passphrase)
	if passphrase == "" {
		for _, grip := range keygrips {
			if val, _ := ssGpgLookup(map[string]string{
				"xdg:schema": ssSchemaPassphrase,
				"keygrip":    grip,
			}); val != "" {
				passphrase = val
				fmt.Fprintf(os.Stderr, "  Reusing passphrase from stored keygrip %s...\n", grip[:16])
				break
			}
		}
	}

	if passphrase == "" {
		if !term.IsTerminal(int(os.Stdin.Fd())) {
			fmt.Fprintln(os.Stderr, "  No passphrase available — use: charly secrets gpg setup --prompt-passphrase")
			return nil
		}
		fmt.Fprint(os.Stderr, "  Enter GPG passphrase to store in Secret Service: ")
		pw, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr, "")
		if err != nil {
			return fmt.Errorf("reading passphrase: %w", err)
		}
		passphrase = string(pw)
		if passphrase == "" {
			fmt.Fprintln(os.Stderr, "  Empty passphrase — use: charly secrets gpg setup --prompt-passphrase")
			return nil
		}
	}

	// Store passphrase for ALL keygrips (primary + subkeys)
	for _, grip := range keygrips {
		label := fmt.Sprintf("GPG Passphrase: %s (keygrip: %s)", keyID, grip[:8])
		if err := ssGpgStore(label, passphrase, map[string]string{
			"xdg:schema": ssSchemaPassphrase,
			"keygrip":    grip,
		}); err != nil {
			return fmt.Errorf("storing passphrase for keygrip %s: %w", grip[:16], err)
		}
		fmt.Fprintf(os.Stderr, "  Stored passphrase for keygrip %s...\n", grip[:16])
	}
	return nil
}

func (c *SecretsGpgSetupCmd) verify() error {
	keyID, err := autoSelectKeyID(c.KeyID)
	if err != nil {
		return err
	}

	// Preset passphrases in agent cache from Secret Service (avoids pinentry GUI)
	presetPassphrasesFromSS(keyID)

	// Encrypt a test string
	testStr := "charly-secrets-gpg-setup-verify"
	encCmd := exec.Command("gpg", "--encrypt", "--armor", "--batch", "--yes", "-r", keyID)
	encCmd.Stdin = strings.NewReader(testStr)
	var encOut bytes.Buffer
	encCmd.Stdout = &encOut
	encCmd.Stderr = nil
	if err := encCmd.Run(); err != nil {
		return fmt.Errorf("test encryption: %w", err)
	}

	// Decrypt it
	decCmd := exec.Command("gpg", "--quiet", "--batch", "--decrypt")
	decCmd.Stdin = &encOut
	decOut, err := decCmd.Output()
	if err != nil {
		return fmt.Errorf("test decryption: %w", err)
	}

	if strings.TrimSpace(string(decOut)) != testStr {
		return fmt.Errorf("round-trip mismatch")
	}
	return nil
}

// presetPassphrasesFromSS injects passphrases from Secret Service into gpg-agent cache.
// Uses gpg-preset-passphrase which bypasses pinentry entirely.
func presetPassphrasesFromSS(keyID string) {
	presetBin, err := exec.LookPath("gpg-preset-passphrase")
	if err != nil {
		// Try common locations
		for _, path := range []string{"/usr/libexec/gpg-preset-passphrase", "/usr/lib/gnupg/gpg-preset-passphrase"} {
			if _, statErr := os.Stat(path); statErr == nil {
				presetBin = path
				break
			}
		}
	}
	if presetBin == "" {
		return
	}

	keys := getSecretKeys()
	for _, k := range keys {
		if k.KeyID != keyID {
			continue
		}
		for _, grip := range k.Keygrips {
			passphrase, _ := ssGpgLookup(map[string]string{
				"xdg:schema": ssSchemaPassphrase,
				"keygrip":    grip,
			})
			if passphrase == "" {
				continue
			}
			cmd := exec.Command(presetBin, "--preset", grip)
			cmd.Stdin = strings.NewReader(passphrase)
			cmd.Stderr = nil
			_ = cmd.Run()
		}
	}
}

// ── doctor command ──────────────────────────────────────────────────

type SecretsGpgDoctorCmd struct {
	File string `short:"f" long:"file" default:".secrets" help:"Encrypted file to check"`
}

//nolint:gocyclo // diagnostic checker: 10 independent peer health checks in a cohesive ok()/warn() narrative; extraction fragments the diagnostic flow
func (c *SecretsGpgDoctorCmd) Run() error {
	failures := 0
	warn := func(msg string) { failures++; fmt.Fprintln(os.Stderr, msg) }
	ok := func(msg string) { fmt.Fprintln(os.Stderr, msg) }

	fmt.Fprintln(os.Stderr, "charly secrets gpg doctor")
	fmt.Fprintln(os.Stderr, "")

	// 1. GPG binary
	if _, err := exec.LookPath("gpg"); err != nil {
		warn("  gpg:                     MISSING")
		return fmt.Errorf("gpg not found")
	}
	ok(fmt.Sprintf("  gpg:                     %s", gpgVersion()))

	// 2. gpg-agent
	if checkAgentRunning() {
		ok("  gpg-agent:               running")
	} else {
		warn("  gpg-agent:               NOT running")
	}

	// 3. gpg-agent.conf
	home, _ := os.UserHomeDir()
	confPath := filepath.Join(home, ".gnupg", "gpg-agent.conf")
	if confData, err := os.ReadFile(confPath); err == nil {
		confStr := string(confData)
		// Check pinentry
		if strings.Contains(confStr, "pinentry-program") {
			re := regexp.MustCompile(`pinentry-program\s+(\S+)`)
			if m := re.FindStringSubmatch(confStr); len(m) > 1 {
				hasLS := ""
				if pinentryHasLibsecret() {
					hasLS = " (libsecret: yes)"
				}
				ok(fmt.Sprintf("  gpg-agent.conf:          %s", confPath))
				ok(fmt.Sprintf("    pinentry:              %s%s", m[1], hasLS))
			}
		} else {
			warn("  gpg-agent.conf:          no pinentry configured")
		}
		// Check cache TTL
		if strings.Contains(confStr, "default-cache-ttl") {
			re := regexp.MustCompile(`default-cache-ttl\s+(\d+)`)
			maxRe := regexp.MustCompile(`max-cache-ttl\s+(\d+)`)
			defTTL := "?"
			maxTTL := "?"
			if m := re.FindStringSubmatch(confStr); len(m) > 1 {
				defTTL = m[1] + "s"
			}
			if m := maxRe.FindStringSubmatch(confStr); len(m) > 1 {
				maxTTL = m[1] + "s"
			}
			ok(fmt.Sprintf("    cache TTL:             %s default / %s max", defTTL, maxTTL))
		}
	} else {
		warn("  gpg-agent.conf:          MISSING (run: charly secrets gpg setup)")
	}

	// 4. Systemd sockets
	socketOK := true
	for _, unit := range []string{"gpg-agent.socket", "gpg-agent-extra.socket"} {
		check := exec.Command("systemctl", "--user", "is-enabled", unit)
		if err := check.Run(); err != nil {
			socketOK = false
		}
	}
	if socketOK {
		ok("  systemd sockets:         enabled")
	} else {
		warn("  systemd sockets:         NOT enabled (run: charly secrets gpg setup)")
	}

	// 5. Secret keys
	keys := getSecretKeys()
	if len(keys) == 0 {
		warn("  secret keys:             NONE")
	} else {
		for _, k := range keys {
			expiry := ""
			if k.Expires != "" {
				expiry = ", expires " + formatGPGDate(k.Expires)
			}
			ok(fmt.Sprintf("  secret keys:             %s (%s%s)", k.KeyID, k.UID, expiry))
		}
	}

	// 6. Secret Service
	if checkSecretServiceAvailable() {
		ok("  Secret Service:          available")

		// 7. Passphrase storage (check ALL keygrips — primary + subkeys)
		for _, k := range keys {
			for i, grip := range k.Keygrips {
				role := "primary"
				if i > 0 {
					role = "subkey"
				}
				if val, _ := ssGpgLookup(map[string]string{
					"xdg:schema": ssSchemaPassphrase,
					"keygrip":    grip,
				}); val != "" {
					ok(fmt.Sprintf("    passphrase (%s):   stored (keygrip: %s...)", role, grip[:16]))
				} else {
					warn(fmt.Sprintf("    passphrase (%s):   NOT stored (keygrip: %s...) — run: charly secrets gpg setup", role, grip[:16]))
				}
			}
		}

		// 8. Key backup
		keyEntries, _ := ssGpgSearch(map[string]string{"xdg:schema": ssSchemaKey})
		if len(keyEntries) > 0 {
			collections := map[string]bool{}
			for _, e := range keyEntries {
				collections[string(e.Path)] = true
			}
			ok(fmt.Sprintf("    key backups:           %d key(s) stored across %d collection-path(s)", len(keyEntries), len(collections)))
		} else {
			ok("    key backups:           none (use: charly secrets gpg export-key --to-keystore)")
		}
	} else {
		warn("  Secret Service:          NOT available")
	}

	// 9. .secrets file
	fmt.Fprintln(os.Stderr, "")
	if _, err := os.Stat(c.File); err != nil {
		ok(fmt.Sprintf("  %s:           not found (OK if no project secrets needed)", c.File))
	} else {
		// Preset passphrases from Secret Service BEFORE listRecipients —
		// modern gpg --list-packets decrypts the outer envelope to enumerate
		// inner packets, which needs the passphrase. Without preset, gpg
		// invokes pinentry, fails when the doctor runs in a context with
		// no display, exits non-zero, and listRecipients returns empty.
		for _, k := range keys {
			presetPassphrasesFromSS(k.KeyID)
		}

		recipients, _ := listRecipients(c.File)
		if len(recipients) > 0 {
			for _, r := range recipients {
				available := "NO"
				if keyExistsInKeyring(r) {
					available = "yes"
				}
				ok(fmt.Sprintf("  %s:           encrypted for %s (key available: %s)", c.File, r, available))
				if available == "NO" {
					failures++
				}
			}
		} else {
			warn(fmt.Sprintf("  %s:           could not read recipients", c.File))
		}

		// 10. Decrypt test
		plaintext, err := gpgDecryptToBytes(c.File)
		if err == nil && len(plaintext) > 0 {
			ok(fmt.Sprintf("  decrypt test:            OK (%d bytes)", len(plaintext)))
		} else if err != nil {
			// Diagnostics already printed by gpgDecryptToBytes
			failures++
		}
	}

	fmt.Fprintln(os.Stderr, "")
	if failures > 0 {
		return fmt.Errorf("%d issue(s) found", failures)
	}
	fmt.Fprintln(os.Stderr, "All checks passed.")
	return nil
}
