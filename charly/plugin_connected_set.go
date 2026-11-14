package main

import (
	"sync"

	"github.com/opencharly/spec/spec"
)

// plugin_connected_set.go — the CONNECTED PLUGIN SET: what the loader ACTUALLY served, per
// plugin, at the moment it connected it (opencharly/charly#1131).
//
// The mapping existed and was thrown away. loadPluginUnit resolves the served provider binary
// and knows the candy's source dir, prints both (`plugin <ref>: served from <path> (build
// <sha256>); source <dir>`), registers the unit's providers — and returned nothing. So the one
// question a consumer could not answer without guessing was "WHICH built binary serves this
// plugin?", and the only way left to answer it was a directory scan with a newest-wins
// tie-break: the `pluginStoreDir()` search that candy/plugin-deploy-firecracker's
// `step:plugin-tree` carried, which is the defect #1131 exists to delete rather than to
// relocate.
//
// This registry is the answer, and it is PROCESS state on purpose: it records what THIS process
// connected, which is exactly the set the deploy's `deploy-plugins-connect` seam resolved. Two
// properties make that safe:
//
//   - OVER-LOAD is harmless. A consumer selects the entries it was authored with (by leaf), so
//     an extra entry is ignored; the collection errs toward include, exactly like
//     collectReferencedPluginWords.
//   - It is never the answer to "is this plugin available". That question belongs to the
//     loader's search rules (CHARLY_PLUGIN_DIR, the FHS dir, the project scan). The registry
//     only reports what a connect already served.
type connectedPlugins struct {
	mu      sync.RWMutex
	entries []spec.DeployConnectedPlugin
}

// connectedPluginSet is the process-wide registry. Its only writer is the loader's connect
// (recordConnectedPluginFromLoad) and its readers are the seams that hand the set on: the
// `deploy-plugins-connect` reply and the step-input injection.
var connectedPluginSet = &connectedPlugins{}

// recordOne adds one served plugin, idempotently on (key, leaf, view_path). A same-key entry
// with a DIFFERENT served binary is kept as its own entry rather than overwritten: it is a
// second build the process connected, and choosing between them is not this registry's call.
func (c *connectedPlugins) recordOne(e spec.DeployConnectedPlugin) {
	if e.Key == "" || e.Leaf == "" || e.ViewPath == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, have := range c.entries {
		if have.Key == e.Key && have.Leaf == e.Leaf && have.ViewPath == e.ViewPath {
			return
		}
	}
	c.entries = append(c.entries, e)
}

// snapshot returns a COPY, in load order, so a caller can never mutate the registry through the
// slice it was handed.
func (c *connectedPlugins) snapshot() []spec.DeployConnectedPlugin {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if len(c.entries) == 0 {
		return nil
	}
	out := make([]spec.DeployConnectedPlugin, len(c.entries))
	copy(out, c.entries)
	return out
}

// forget clears the registry. TEST-ONLY: a Go test that connects a plugin fixture owns the
// process, and leaving one test's entries visible to the next would make an assertion about the
// connected set depend on test order.
func (c *connectedPlugins) forget() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = nil
}

// recordConnectedPluginFromLoad records the unit loadPluginUnit just connected. leaf is derived,
// never passed: the FILENAME a baked plugin binary takes is bakedPluginFileName's rule (the key's
// last path segment, flattened), and the loader opens the delivered binary by exactly that name —
// so a second derivation here would be a second rule for one filename (R2).
//
// providers are the words the unit REGISTERED, rendered by the ONE identity renderer the rest of
// the loader uses. They are load-bearing for a delivered tree: the loader resolves a bare
// command/verb word through a `<leaf>.providers` manifest beside the binary, so a tree without the
// manifest answers every invocation with charly's usage text (measured on a disposable guest,
// opencharly/charly#1131). They come from the unit the engine connected — never from scraping the
// plugin's binary or re-parsing its candy.
func recordConnectedPluginFromLoad(key, viewPath, srcDir string, providers []string) {
	connectedPluginSet.recordOne(spec.DeployConnectedPlugin{
		Key:       key,
		Leaf:      bakedPluginFileName(key),
		ViewPath:  viewPath,
		SrcDir:    srcDir,
		Providers: providers,
	})
}
