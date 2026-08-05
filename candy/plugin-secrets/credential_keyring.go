package secrets

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/zalando/go-keyring"
)

// KeyringState represents the detected state of the system keyring.
type KeyringState int

const (
	KeyringUnchecked   KeyringState = iota // Not yet probed
	KeyringAvailable                       // Unlocked and functional
	KeyringLocked                          // Present but locked (requires unlock prompt)
	KeyringUnavailable                     // Not present or broken
)

// String returns a human-readable keyring state.
func (s KeyringState) String() string {
	switch s {
	case KeyringAvailable:
		return "available"
	case KeyringLocked:
		return "locked"
	case KeyringUnavailable:
		return "unavailable"
	default:
		return "unchecked"
	}
}

var (
	keyringStateMu  sync.Mutex
	keyringStateVal KeyringState
)

// GetKeyringState returns the last detected keyring state.
func GetKeyringState() KeyringState {
	keyringStateMu.Lock()
	defer keyringStateMu.Unlock()
	return keyringStateVal
}

func setKeyringState(state KeyringState) {
	keyringStateMu.Lock()
	defer keyringStateMu.Unlock()
	keyringStateVal = state
}

// resetKeyringState resets state (for testing).
func resetKeyringState() {
	setKeyringState(KeyringUnchecked)
}

// KeyringStore implements CredentialStore using the system keyring
// (freedesktop Secret Service on Linux, Keychain on macOS).
type KeyringStore struct{}

const keyringTimeout = 3 * time.Second

// Probe tests whether the system keyring is usable.
//
// Unlike the old direct go-keyring probe (which used only the `default` alias
// and failed hard when that alias pointed at a broken collection), this
// implementation opens a direct Secret Service connection and considers the
// keyring "available" as long as at least ONE collection can be reached and
// responds to property reads. This makes charly resilient to Secret Service
// providers like KeePassXC's FdoSecrets plugin that occasionally advertise
// broken stub collections alongside working ones.
//
// Returns:
//   - nil + KeyringAvailable: at least one healthy collection exists.
//   - KeyringLockedError + KeyringLocked: the probe timed out (likely waiting
//     on an unlock prompt).
//   - plain error + KeyringUnavailable: no DBus session bus, no collections,
//     or every collection is broken.
func (k *KeyringStore) Probe() error {
	type result struct {
		healthy int
		broken  int
		err     error
	}
	ch := make(chan result, 1)
	go func() {
		c, err := newSSClient()
		if err != nil {
			ch <- result{err: err}
			return
		}
		defer c.close()
		paths, err := c.collections()
		if err != nil {
			ch <- result{err: err}
			return
		}
		var r result
		for _, p := range paths {
			if herr := c.isCollectionHealthy(p); herr != nil {
				r.broken++
				continue
			}
			r.healthy++
		}
		if r.healthy == 0 {
			if r.broken > 0 {
				r.err = fmt.Errorf("%d collection(s) present, all broken", r.broken)
			} else {
				r.err = fmt.Errorf("no collections present")
			}
		}
		ch <- r
	}()

	select {
	case r := <-ch:
		if r.err != nil {
			setKeyringState(KeyringUnavailable)
			return fmt.Errorf("keyring unavailable: %w", r.err)
		}
		setKeyringState(KeyringAvailable)
		return nil
	case <-time.After(keyringTimeout):
		setKeyringState(KeyringLocked)
		return fmt.Errorf("keyring probe timed out — unlock your keyring or run: charly config set secret_backend config")
	}
}

func (k *KeyringStore) Get(service, key string) (string, error) {
	if GetKeyringState() == KeyringLocked {
		return "", &KeyringLockedError{op: "get", service: service, key: key}
	}
	type result struct {
		val string
		err error
	}
	ch := make(chan result, 1)
	go func() {
		v, e := keyringGetViaSSClient(service, key)
		ch <- result{v, e}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			if isKeyringNotFound(r.err) {
				// Not found anywhere. Fix H: warn if the shadow index claims
				// this key should exist — something has desynchronized.
				if isIndexed, _ := isKeyringIndexed(service, key); isIndexed {
					fmt.Fprintf(os.Stderr,
						"charly: warning: %s/%s is listed in the keyring shadow index but not present in any Secret Service collection. "+
							"Run `charly secrets prune` to reconcile (or store it again with `charly secrets set %s %s`).\n",
						service, key, service, key)
				}
				return "", nil
			}
			if errors.Is(r.err, ErrSSInteractiveUnlockRequired) {
				setKeyringState(KeyringLocked)
				return "", &KeyringLockedError{op: "get", service: service, key: key}
			}
			return "", fmt.Errorf("keyring get %s/%s: %w", service, key, r.err)
		}
		return r.val, nil
	case <-time.After(keyringTimeout):
		setKeyringState(KeyringLocked)
		return "", &KeyringLockedError{op: "get", service: service, key: key}
	}
}

// keyringGetViaSSClient performs a credential read via the iteration-capable
// ssClient instead of zalando/go-keyring's hardcoded-default-alias path. This
// is the core of defect A's fix.
//
// Returns the secret value on success; ("", ErrSSNotFound) when the credential
// is not stored in any reachable collection; or a wrapped DBus error.
func keyringGetViaSSClient(service, key string) (string, error) {
	c, err := newSSClient()
	if err != nil {
		return "", fmt.Errorf("opening secret service: %w", err)
	}
	defer c.close()

	preferLabel := ""
	if cfg, err := LoadRuntimeConfig(); err == nil {
		preferLabel = cfg.KeyringCollectionLabel
	}

	item, _, err := c.findItemByAttrsAnyCollection(map[string]string{"service": service, "username": key}, preferLabel)
	if err != nil {
		return "", err
	}
	secret, err := c.getSecret(item)
	if err != nil {
		return "", fmt.Errorf("reading secret value: %w", err)
	}
	return string(secret), nil
}

// isKeyringIndexed reports whether a key is present in the shadow index
// maintained in config.yml. Used by Get to warn about index/reality drift.
func isKeyringIndexed(service, key string) (bool, error) {
	cfg, err := LoadRuntimeConfig()
	if err != nil {
		return false, err
	}
	entry := service + "/" + key
	if slices.Contains(cfg.KeyringKeys, entry) {
		return true, nil
	}
	return false, nil
}

func (k *KeyringStore) Set(service, key, value string) error {
	if GetKeyringState() == KeyringLocked {
		return &KeyringLockedError{op: "set", service: service, key: key}
	}
	ch := make(chan error, 1)
	go func() {
		ch <- keyring.Set(service, key, value)
	}()
	select {
	case err := <-ch:
		if err != nil {
			return fmt.Errorf("keyring set %s/%s: %w", service, key, err)
		}
		return addKeyringIndex(service, key)
	case <-time.After(keyringTimeout):
		setKeyringState(KeyringLocked)
		return &KeyringLockedError{op: "set", service: service, key: key}
	}
}

func (k *KeyringStore) Delete(service, key string) error {
	if GetKeyringState() == KeyringLocked {
		return &KeyringLockedError{op: "delete", service: service, key: key}
	}
	ch := make(chan error, 1)
	go func() {
		ch <- keyring.Delete(service, key)
	}()
	select {
	case err := <-ch:
		if err != nil && !isKeyringNotFound(err) {
			return fmt.Errorf("keyring delete %s/%s: %w", service, key, err)
		}
		return removeKeyringIndex(service, key)
	case <-time.After(keyringTimeout):
		setKeyringState(KeyringLocked)
		return &KeyringLockedError{op: "delete", service: service, key: key}
	}
}

// List returns all keys for a service from the shadow index.
// The go-keyring library does not support listing, so we maintain
// a key-name-only index in config.yml (no secret values stored).
func (k *KeyringStore) List(service string) ([]string, error) {
	cfg, err := LoadRuntimeConfig()
	if err != nil {
		return nil, err
	}
	prefix := service + "/"
	var keys []string
	for _, entry := range cfg.KeyringKeys {
		if after, ok := strings.CutPrefix(entry, prefix); ok {
			keys = append(keys, after)
		}
	}
	return keys, nil
}

func (k *KeyringStore) Name() string {
	if GetKeyringState() == KeyringLocked {
		return "keyring (locked)"
	}
	return "keyring"
}

// KeyringLockedError is returned when a keyring operation cannot proceed
// because the keyring is locked. Callers can check for this with
// errors.As to provide context-specific messages.
type KeyringLockedError struct {
	op      string // "get", "set", "delete"
	service string
	key     string
}

func (e *KeyringLockedError) Error() string {
	return fmt.Sprintf("keyring is locked (cannot %s %s/%s) — unlock your keyring or run: charly config set secret_backend config", e.op, e.service, e.key)
}

// IsKeyringLocked returns true if the error is a KeyringLockedError.
func IsKeyringLocked(err error) bool {
	var locked *KeyringLockedError
	return err != nil && errors.As(err, &locked)
}

// isKeyringNotFound checks whether the error indicates the key was not found.
// Accepts three forms of "not found":
//   - ErrSSNotFound from our ssClient (authoritative miss across all
//     reachable collections)
//   - keyring.ErrNotFound from zalando/go-keyring (still used by Set/Delete)
//   - any error whose message contains "secret not found" (legacy catchall
//     for error strings from other layers)
func isKeyringNotFound(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrSSNotFound) {
		return true
	}
	if errors.Is(err, keyring.ErrNotFound) {
		return true
	}
	return strings.Contains(err.Error(), "secret not found")
}

// addKeyringIndex adds a service/key entry to the shadow index in config.yml.
func addKeyringIndex(service, key string) error {
	cfg, err := LoadRuntimeConfig()
	if err != nil {
		return err
	}
	entry := service + "/" + key
	if slices.Contains(cfg.KeyringKeys, entry) {
		return nil
	}
	cfg.KeyringKeys = append(cfg.KeyringKeys, entry)
	slices.Sort(cfg.KeyringKeys)
	return SaveRuntimeConfig(cfg)
}

// removeKeyringIndex removes a service/key entry from the shadow index.
func removeKeyringIndex(service, key string) error {
	cfg, err := LoadRuntimeConfig()
	if err != nil {
		return err
	}
	entry := service + "/" + key
	idx := slices.Index(cfg.KeyringKeys, entry)
	if idx < 0 {
		return nil
	}
	cfg.KeyringKeys = slices.Delete(cfg.KeyringKeys, idx, idx+1)
	return SaveRuntimeConfig(cfg)
}
