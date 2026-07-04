package secrets

import (
	"errors"
	"fmt"
	"os"

	dbus "github.com/godbus/dbus/v5"
)

const (
	ssServiceName         = "org.freedesktop.secrets"
	ssServicePath         = "/org/freedesktop/secrets"
	ssAliasesBasePath     = "/org/freedesktop/secrets/aliases/"
	ssServiceInterface    = "org.freedesktop.Secret.Service"
	ssCollectionInterface = "org.freedesktop.Secret.Collection"
	ssItemInterface       = "org.freedesktop.Secret.Item"
	ssSessionInterface    = "org.freedesktop.Secret.Session"
)

// ErrSSNotFound is returned when a credential is not found in any reachable
// collection. Distinct from ErrSSAllBroken (no collection is reachable).
var ErrSSNotFound = errors.New("secret not found in any collection")

// ErrSSAllBroken is returned when every reachable collection errors during
// property/search/unlock. Means the secret service itself is unusable for
// reads, not that the secret simply isn't stored.
var ErrSSAllBroken = errors.New("all secret-service collections are unreachable")

// ErrSSInteractiveUnlockRequired is returned when every candidate collection
// is locked and unlocking requires an interactive prompt (which is not
// available in non-interactive code paths like systemd ExecStartPre).
// Distinct from ErrSSAllBroken: the collections are functional, the
// credential likely exists, we just can't read it until the user unlocks.
var ErrSSInteractiveUnlockRequired = errors.New("secret service collections require interactive unlock")

// ssSecret mirrors the (oayays) dbus signature for org.freedesktop.Secret.Item.GetSecret.
type ssSecret struct {
	Session     dbus.ObjectPath
	Parameters  []byte
	Value       []byte
	ContentType string `dbus:"content_type"`
}

// ssOps is the minimal set of Secret Service operations used by
// findItemByAttrsAcrossCollections and resolveTargetCollection. Defining it as
// an interface lets tests supply a fake implementation without touching DBus.
type ssOps interface {
	readAlias(name string) (dbus.ObjectPath, error)
	collections() ([]dbus.ObjectPath, error)
	isCollectionHealthy(path dbus.ObjectPath) error
	collectionLabel(path dbus.ObjectPath) string
	unlock(path dbus.ObjectPath) error
	searchItemByAttrs(path dbus.ObjectPath, attrs map[string]string) (dbus.ObjectPath, error)
	searchItemsByAttrs(path dbus.ObjectPath, attrs map[string]string) ([]dbus.ObjectPath, error)
	getSecret(item dbus.ObjectPath) ([]byte, error)
	itemMetadata(item dbus.ObjectPath) (label string, attrs map[string]string, err error)
	createItem(path dbus.ObjectPath, attrs map[string]string, secret []byte, label string, replace bool) (dbus.ObjectPath, error)
	deleteItem(item dbus.ObjectPath) error
}

// ssClient is a minimal godbus-based Secret Service client focused on the
// credential-read path. It intentionally supports iteration across
// collections (unlike zalando/go-keyring, which is hardcoded to the default
// alias) so charly can tolerate broken Secret Service providers that advertise
// stub collections. ssClient implements ssOps.
type ssClient struct {
	conn    *dbus.Conn
	service dbus.BusObject
	session dbus.ObjectPath
}

// newSSClient opens a session bus connection and a Secret Service session.
func newSSClient() (*ssClient, error) {
	conn, err := dbus.SessionBus()
	if err != nil {
		return nil, fmt.Errorf("connecting to session bus: %w", err)
	}
	service := conn.Object(ssServiceName, ssServicePath)

	var algorithm dbus.Variant
	var sessionPath dbus.ObjectPath
	call := service.Call(
		ssServiceInterface+".OpenSession", 0,
		"plain", dbus.MakeVariant(""),
	)
	if call.Err != nil {
		return nil, fmt.Errorf("opening secret service session: %w", call.Err)
	}
	if err := call.Store(&algorithm, &sessionPath); err != nil {
		return nil, fmt.Errorf("decoding secret service session: %w", err)
	}
	_ = algorithm
	return &ssClient{conn: conn, service: service, session: sessionPath}, nil
}

// close closes the Secret Service session. The underlying dbus connection
// is owned by the process (dbus.SessionBus is a singleton) so we do not
// close it here.
func (c *ssClient) close() {
	if c.session == "" {
		return
	}
	_ = c.conn.Object(ssServiceName, c.session).Call(ssSessionInterface+".Close", 0).Err
	c.session = ""
}

// collections returns the list of collection object paths advertised by the
// Secret Service, in the order they are reported.
func (c *ssClient) collections() ([]dbus.ObjectPath, error) {
	v, err := c.service.GetProperty(ssServiceInterface + ".Collections")
	if err != nil {
		return nil, fmt.Errorf("reading Collections property: %w", err)
	}
	paths, ok := v.Value().([]dbus.ObjectPath)
	if !ok {
		return nil, fmt.Errorf("unexpected type %T for Collections property", v.Value())
	}
	return paths, nil
}

// readAlias resolves a Secret Service alias name (e.g. "default", "login") to
// the object path of the collection it points at. Returns an empty path with
// nil error if the alias is unset (dbus "/" sentinel).
func (c *ssClient) readAlias(name string) (dbus.ObjectPath, error) {
	var path dbus.ObjectPath
	call := c.service.Call(ssServiceInterface+".ReadAlias", 0, name)
	if call.Err != nil {
		return "", fmt.Errorf("ReadAlias(%q): %w", name, call.Err)
	}
	if err := call.Store(&path); err != nil {
		return "", fmt.Errorf("decoding ReadAlias(%q): %w", name, err)
	}
	if path == "/" {
		return "", nil
	}
	return path, nil
}

// collectionLabel returns the Label property of a collection, or "" if it is
// unreadable (broken collection).
func (c *ssClient) collectionLabel(path dbus.ObjectPath) string {
	obj := c.conn.Object(ssServiceName, path)
	v, err := obj.GetProperty(ssCollectionInterface + ".Label")
	if err != nil {
		return ""
	}
	s, _ := v.Value().(string)
	return s
}

// isCollectionHealthy probes a collection by reading its Label property.
// A healthy collection returns nil; a broken stub returns the underlying
// dbus error.
func (c *ssClient) isCollectionHealthy(path dbus.ObjectPath) error {
	obj := c.conn.Object(ssServiceName, path)
	_, err := obj.GetProperty(ssCollectionInterface + ".Label")
	if err != nil {
		return fmt.Errorf("collection %s unhealthy: %w", path, err)
	}
	return nil
}

// unlock attempts to unlock a collection. Returns nil if already unlocked or
// unlock succeeds without a prompt. Returns an error if a prompt is required
// (this path is for non-interactive lookup — prompting would block systemd
// ExecStartPre) or the call fails.
func (c *ssClient) unlock(path dbus.ObjectPath) error {
	var unlocked []dbus.ObjectPath
	var prompt dbus.ObjectPath
	call := c.service.Call(ssServiceInterface+".Unlock", 0, []dbus.ObjectPath{path})
	if call.Err != nil {
		return fmt.Errorf("Unlock(%s): %w", path, call.Err)
	}
	if err := call.Store(&unlocked, &prompt); err != nil {
		return fmt.Errorf("decoding Unlock(%s): %w", path, err)
	}
	if prompt != dbus.ObjectPath("/") {
		return fmt.Errorf("%w: %s", ErrSSInteractiveUnlockRequired, path)
	}
	return nil
}

// searchItemByAttrs finds a single item matching arbitrary string attributes
// in a specific collection. The attrs map is passed verbatim to libsecret's
// Collection.SearchItems (Dict<String,String>). Returns ErrSSNotFound if no
// match, or the dbus error if the SearchItems call fails.
func (c *ssClient) searchItemByAttrs(collectionPath dbus.ObjectPath, attrs map[string]string) (dbus.ObjectPath, error) {
	results, err := c.searchItemsByAttrs(collectionPath, attrs)
	if err != nil {
		return "", err
	}
	if len(results) == 0 {
		return "", ErrSSNotFound
	}
	return results[0], nil
}

// searchItemsByAttrs returns ALL items matching attrs in a specific
// collection. Used when callers need to enumerate (e.g. count backed-up GPG
// keys) rather than fetch the first match.
func (c *ssClient) searchItemsByAttrs(collectionPath dbus.ObjectPath, attrs map[string]string) ([]dbus.ObjectPath, error) {
	obj := c.conn.Object(ssServiceName, collectionPath)
	var results []dbus.ObjectPath
	call := obj.Call(ssCollectionInterface+".SearchItems", 0, attrs)
	if call.Err != nil {
		return nil, fmt.Errorf("SearchItems on %s: %w", collectionPath, call.Err)
	}
	if err := call.Store(&results); err != nil {
		return nil, fmt.Errorf("decoding SearchItems on %s: %w", collectionPath, err)
	}
	return results, nil
}

// itemMetadata reads an item's Label and Attributes properties.
func (c *ssClient) itemMetadata(item dbus.ObjectPath) (string, map[string]string, error) {
	obj := c.conn.Object(ssServiceName, item)
	labelV, err := obj.GetProperty(ssItemInterface + ".Label")
	if err != nil {
		return "", nil, fmt.Errorf("reading Label of %s: %w", item, err)
	}
	label, _ := labelV.Value().(string)
	attrsV, err := obj.GetProperty(ssItemInterface + ".Attributes")
	if err != nil {
		return label, nil, fmt.Errorf("reading Attributes of %s: %w", item, err)
	}
	attrs, _ := attrsV.Value().(map[string]string)
	if attrs == nil {
		attrs = map[string]string{}
	}
	return label, attrs, nil
}

// getSecret retrieves the secret value of an item under the client's session.
func (c *ssClient) getSecret(item dbus.ObjectPath) ([]byte, error) {
	obj := c.conn.Object(ssServiceName, item)
	var secret ssSecret
	call := obj.Call(ssItemInterface+".GetSecret", 0, c.session)
	if call.Err != nil {
		return nil, fmt.Errorf("GetSecret(%s): %w", item, call.Err)
	}
	if err := call.Store(&secret); err != nil {
		return nil, fmt.Errorf("decoding GetSecret(%s): %w", item, err)
	}
	return secret.Value, nil
}

// createItem creates a new item in the named collection with the given
// attributes, secret payload, and label. When replace is true and an item with
// matching attributes already exists, libsecret replaces it; otherwise the
// caller gets back a separate item path. Returns the new (or replacing) item's
// object path. If the collection requires unlock, the caller must unlock it
// first via c.unlock(collectionPath).
func (c *ssClient) createItem(collectionPath dbus.ObjectPath, attrs map[string]string, secret []byte, label string, replace bool) (dbus.ObjectPath, error) {
	obj := c.conn.Object(ssServiceName, collectionPath)
	properties := map[string]dbus.Variant{
		ssItemInterface + ".Label":      dbus.MakeVariant(label),
		ssItemInterface + ".Attributes": dbus.MakeVariant(attrs),
	}
	secretStruct := ssSecret{
		Session:     c.session,
		Parameters:  []byte{},
		Value:       secret,
		ContentType: "text/plain",
	}
	var item dbus.ObjectPath
	var prompt dbus.ObjectPath
	call := obj.Call(ssCollectionInterface+".CreateItem", 0, properties, secretStruct, replace)
	if call.Err != nil {
		return "", fmt.Errorf("CreateItem on %s: %w", collectionPath, call.Err)
	}
	if err := call.Store(&item, &prompt); err != nil {
		return "", fmt.Errorf("decoding CreateItem on %s: %w", collectionPath, err)
	}
	if prompt != dbus.ObjectPath("/") {
		return "", fmt.Errorf("CreateItem on %s requires interactive prompt %s", collectionPath, prompt)
	}
	return item, nil
}

// deleteItem removes an item by its object path.
func (c *ssClient) deleteItem(item dbus.ObjectPath) error {
	obj := c.conn.Object(ssServiceName, item)
	var prompt dbus.ObjectPath
	call := obj.Call(ssItemInterface+".Delete", 0)
	if call.Err != nil {
		return fmt.Errorf("Delete(%s): %w", item, call.Err)
	}
	if err := call.Store(&prompt); err != nil {
		return fmt.Errorf("decoding Delete(%s): %w", item, err)
	}
	if prompt != dbus.ObjectPath("/") {
		return fmt.Errorf("Delete(%s) requires interactive prompt %s", item, prompt)
	}
	return nil
}

// findItemAnyCollection searches for a credential by iterating Secret Service
// collections in priority order. Legacy {service, username} signature kept for
// existing credential-keyring callers — internally builds the attrs map and
// delegates to findItemByAttrsAcrossCollections.
func (c *ssClient) findItemAnyCollection(service, username, preferLabel string) (dbus.ObjectPath, string, error) {
	return findItemAcrossCollections(c, service, username, preferLabel)
}

// findItemByAttrsAnyCollection is the attribute-map-based read path. Used by
// the GPG keystore code which searches by xdg:schema + keyid (or keygrip).
func (c *ssClient) findItemByAttrsAnyCollection(attrs map[string]string, preferLabel string) (dbus.ObjectPath, string, error) {
	return findItemByAttrsAcrossCollections(c, attrs, preferLabel)
}

// findItemAcrossCollections is the legacy {service, username} wrapper preserved
// so existing credential-keyring tests and call sites do not need to change.
func findItemAcrossCollections(c ssOps, service, username, preferLabel string) (dbus.ObjectPath, string, error) {
	return findItemByAttrsAcrossCollections(c, map[string]string{
		"service":  service,
		"username": username,
	}, preferLabel)
}

// findItemByAttrsAcrossCollections is the testable body of the credential
// iteration. It takes an ssOps interface so unit tests can supply a fake
// implementation. Priority order:
//
//  1. The collection aliased as "default" (if it exists and is healthy)
//  2. A collection matching preferLabel (if non-empty and healthy)
//  3. Every other healthy collection in listing order
//
// Returns the matched item path and the label of the collection that served
// the match. Returns ErrSSNotFound if no collection contains the item but at
// least one was searched successfully. Returns ErrSSAllBroken if every
// candidate collection errored (unreachable, broken, or failed to unlock) —
// distinct from "secret not stored".
//
// Broken collections are skipped with a diagnostic line to stderr so the
// user can see exactly which collection caused the fallback and why.
func findItemByAttrsAcrossCollections(c ssOps, attrs map[string]string, preferLabel string) (dbus.ObjectPath, string, error) {
	tried := make(map[dbus.ObjectPath]bool)
	var candidates []dbus.ObjectPath

	// Priority 1: the collection at the "default" alias.
	if defPath, err := c.readAlias("default"); err == nil && defPath != "" {
		if healthErr := c.isCollectionHealthy(defPath); healthErr == nil {
			candidates = append(candidates, defPath)
			tried[defPath] = true
		} else {
			fmt.Fprintf(os.Stderr,
				"charly: Secret Service default alias target %s is unhealthy; falling back to collection iteration (%v)\n",
				defPath, healthErr)
		}
	}

	// Priority 2: a collection with the preferred label (e.g. set via
	// `charly settings set keyring_collection_label opencharly`).
	if preferLabel != "" {
		if paths, err := c.collections(); err == nil {
			for _, p := range paths {
				if tried[p] {
					continue
				}
				if c.collectionLabel(p) != preferLabel {
					continue
				}
				if err := c.isCollectionHealthy(p); err != nil {
					fmt.Fprintf(os.Stderr,
						"charly: preferred collection %q (%s) is unhealthy: %v\n",
						preferLabel, p, err)
					continue
				}
				candidates = append(candidates, p)
				tried[p] = true
				break
			}
		}
	}

	// Priority 3: every remaining healthy collection.
	paths, err := c.collections()
	if err != nil {
		if len(candidates) == 0 {
			return "", "", fmt.Errorf("listing collections: %w", err)
		}
		// We already have some candidates from priorities 1-2; proceed with what we have.
		fmt.Fprintf(os.Stderr, "charly: cannot list all collections (%v); trying %d priority candidate(s)\n", err, len(candidates))
	} else {
		for _, p := range paths {
			if tried[p] {
				continue
			}
			if herr := c.isCollectionHealthy(p); herr != nil {
				fmt.Fprintf(os.Stderr,
					"charly: skipping broken Secret Service collection %s: %v\n", p, herr)
				continue
			}
			candidates = append(candidates, p)
			tried[p] = true
		}
	}

	if len(candidates) == 0 {
		return "", "", ErrSSAllBroken
	}

	// Try each candidate in priority order. Counts track whether we had at
	// least one successful search (in which case "not found" is authoritative)
	// vs every candidate erroring (ErrSSAllBroken). Locked collections
	// (interactive unlock required) are tracked separately — if every
	// candidate is locked, we return ErrSSInteractiveUnlockRequired so the
	// caller can wait for the user to unlock rather than hard-failing.
	searchErrors := 0
	lockedCount := 0
	for _, p := range candidates {
		label := c.collectionLabel(p)
		if err := c.unlock(p); err != nil {
			if errors.Is(err, ErrSSInteractiveUnlockRequired) {
				lockedCount++
				fmt.Fprintf(os.Stderr,
					"charly: collection %q (%s) is locked — interactive unlock required\n", label, p)
			} else {
				searchErrors++
				fmt.Fprintf(os.Stderr,
					"charly: cannot unlock collection %q (%s): %v\n", label, p, err)
			}
			continue
		}
		item, err := c.searchItemByAttrs(p, attrs)
		if err == nil {
			return item, label, nil
		}
		if errors.Is(err, ErrSSNotFound) {
			continue
		}
		fmt.Fprintf(os.Stderr,
			"charly: search failed on collection %q (%s): %v\n", label, p, err)
		searchErrors++
	}

	if lockedCount > 0 && searchErrors == 0 {
		return "", "", ErrSSInteractiveUnlockRequired
	}
	if searchErrors+lockedCount == len(candidates) {
		return "", "", ErrSSAllBroken
	}
	return "", "", ErrSSNotFound
}

// resolveTargetCollection picks the collection writes should land in. Same
// priority as the read path (default alias → preferLabel → first healthy
// unlocked collection) so writes co-locate with reads — a key written under
// `keyring_collection_label: opencharly` will be readable by a subsequent
// findItemByAttrsAcrossCollections call with the same setting.
//
// Each candidate is unlocked first (non-interactive). Locked collections that
// require an interactive prompt are skipped with a diagnostic; the caller
// gets ErrSSAllBroken / ErrSSInteractiveUnlockRequired as appropriate.
//
// Returns the chosen collection path and its label on success.
func resolveTargetCollection(c ssOps, preferLabel string) (dbus.ObjectPath, string, error) {
	tried := make(map[dbus.ObjectPath]bool)
	var candidates []dbus.ObjectPath

	if defPath, err := c.readAlias("default"); err == nil && defPath != "" {
		if healthErr := c.isCollectionHealthy(defPath); healthErr == nil {
			candidates = append(candidates, defPath)
			tried[defPath] = true
		} else {
			fmt.Fprintf(os.Stderr,
				"charly: Secret Service default alias target %s is unhealthy; falling back to collection iteration (%v)\n",
				defPath, healthErr)
		}
	}

	if preferLabel != "" {
		if paths, err := c.collections(); err == nil {
			for _, p := range paths {
				if tried[p] {
					continue
				}
				if c.collectionLabel(p) != preferLabel {
					continue
				}
				if err := c.isCollectionHealthy(p); err != nil {
					fmt.Fprintf(os.Stderr,
						"charly: preferred collection %q (%s) is unhealthy: %v\n",
						preferLabel, p, err)
					continue
				}
				candidates = append(candidates, p)
				tried[p] = true
				break
			}
		}
	}

	paths, err := c.collections()
	if err != nil {
		if len(candidates) == 0 {
			return "", "", fmt.Errorf("listing collections: %w", err)
		}
		fmt.Fprintf(os.Stderr, "charly: cannot list all collections (%v); trying %d priority candidate(s)\n", err, len(candidates))
	} else {
		for _, p := range paths {
			if tried[p] {
				continue
			}
			if herr := c.isCollectionHealthy(p); herr != nil {
				fmt.Fprintf(os.Stderr,
					"charly: skipping broken Secret Service collection %s: %v\n", p, herr)
				continue
			}
			candidates = append(candidates, p)
			tried[p] = true
		}
	}

	if len(candidates) == 0 {
		return "", "", ErrSSAllBroken
	}

	lockedCount := 0
	for _, p := range candidates {
		label := c.collectionLabel(p)
		if err := c.unlock(p); err != nil {
			if errors.Is(err, ErrSSInteractiveUnlockRequired) {
				lockedCount++
				fmt.Fprintf(os.Stderr,
					"charly: collection %q (%s) is locked — interactive unlock required\n", label, p)
				continue
			}
			fmt.Fprintf(os.Stderr,
				"charly: cannot unlock collection %q (%s): %v\n", label, p, err)
			continue
		}
		return p, label, nil
	}

	if lockedCount == len(candidates) {
		return "", "", ErrSSInteractiveUnlockRequired
	}
	return "", "", ErrSSAllBroken
}
