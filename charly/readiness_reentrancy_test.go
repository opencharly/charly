package main

import (
	"slices"
	"testing"
	"time"
)

// TestLoadedReadiness_ReentrancyGuard locks in the fix for the re-entrant sync.Once deadlock in
// readiness_config.go. loadedReadiness's Once resolver calls LoadUnified, which connects the
// project's kind plugins; each LocalTransport.Connect threads readiness env via readinessPluginEnv
// → loadedReadiness — a SAME-goroutine re-entry into the Once that deadlocks on its internal mutex
// (RCA'd live via `charly doctor` in-project: credentialHealth → connectPluginByWord →
// loadProjectPlugins → Connect → readinessPluginEnv → loadedReadiness → LoadUnified →
// connectDeclaredKindPlugins → … → loadedReadiness). The guard makes a re-entrant call (the
// readinessLoading flag set) return the built-in defaults instead of dead-locking. With the flag
// set, loadedReadiness MUST return promptly with the built-in defaults; without the guard it would
// (re-)enter the Once and hang.
func TestLoadedReadiness_ReentrancyGuard(t *testing.T) {
	readinessLoading.Store(true)
	defer readinessLoading.Store(false)

	done := make(chan []string, 1)
	go func() { done <- loadedReadiness().PluginEnv() }()

	select {
	case got := <-done:
		def, _ := readinessResolve(nil)
		if !slices.Equal(got, def.PluginEnv()) {
			t.Fatalf("re-entrant loadedReadiness PluginEnv = %v, want built-in defaults %v", got, def.PluginEnv())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("loadedReadiness deadlocked with the re-entrancy guard set (readinessLoading=true)")
	}
}
