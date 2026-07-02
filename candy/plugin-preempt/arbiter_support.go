package preempt

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/opencharly/charly/charly/spec"
)

// arbiter_support.go — the arbiter's pure/host-local helpers (cutover C9), moved from charly
// core with the arbiter: GPU-resource poisoning (device_lock-wedge containment, boot-id keyed),
// owner liveness (PID + /proc start-time, PID-reuse guarded), the driver-mode arbitration math,
// and the small set helpers. These are filesystem/OS-local (the ledger dir, /proc) so they run
// directly in the plugin process — no host seam needed.

// --- GPU-resource poisoning (device_lock wedge containment) ----------------------------------

func bootID() string {
	data, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func poisonTokenFileName(token string) string {
	var b strings.Builder
	for _, r := range token {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return "poison-" + b.String() + ".id"
}

func (a *ResourceArbiter) poisonPath(token string) string {
	return filepath.Join(filepath.Dir(a.ledgerPath), poisonTokenFileName(token))
}

// poisonResource marks a GPU token unusable until the next host reboot.
func (a *ResourceArbiter) poisonResource(token string) {
	bid := bootID()
	if bid == "" {
		return
	}
	p := a.poisonPath(token)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return
	}
	_ = os.WriteFile(p, []byte(bid+"\n"), 0o644)
	fmt.Fprintf(os.Stderr, "preempt: POISONED GPU resource %q — driver switch wedged the device_lock; a host reboot is required before it can be claimed again\n", token)
}

// resourcePoisoned reports whether a token is poisoned FOR THE CURRENT BOOT (a prior-boot marker
// is stale — removed + reads not-poisoned).
func (a *ResourceArbiter) resourcePoisoned(token string) bool {
	p := a.poisonPath(token)
	data, err := os.ReadFile(p)
	if err != nil {
		return false
	}
	if marked := strings.TrimSpace(string(data)); marked != "" && marked == bootID() {
		return true
	}
	_ = os.Remove(p)
	return false
}

// clearPoison removes a token's poison marker (`charly vm gpu recover`).
func (a *ResourceArbiter) clearPoison(token string) {
	_ = os.Remove(a.poisonPath(token))
}

// firstPoisonedToken returns the first gpu-backed token in tokens poisoned for the current boot,
// or "" when none. Only gpu-backed tokens (present in the resources map) can wedge.
func (a *ResourceArbiter) firstPoisonedToken(tokens []string) string {
	resources := a.resources()
	for _, tok := range tokens {
		if _, ok := resources[tok]; ok && a.resourcePoisoned(tok) {
			return tok
		}
	}
	return ""
}

// --- owner liveness (crashed-owner reconcile, PID-reuse guarded) -----------------------------

// ownerAlive reports whether the process that created a lease is still running, guarding PID
// reuse by matching the recorded /proc start-time. pid<=0 (a pre-upgrade lease) reads not-alive.
func ownerAlive(pid int, start string) bool {
	if pid <= 0 {
		return false
	}
	st, err := procStartTime(pid)
	if err != nil {
		return false
	}
	return start == "" || st == start
}

// procStartTime returns a process's kernel start-time (field 22 of /proc/<pid>/stat).
func procStartTime(pid int) (string, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return "", err
	}
	s := string(data)
	rp := strings.LastIndexByte(s, ')')
	if rp < 0 || rp+2 > len(s) {
		return "", fmt.Errorf("malformed /proc/%d/stat", pid)
	}
	f := strings.Fields(s[rp+2:])
	const startIdx = 19 // field 22 overall = index 19 counting from field 3 (state)
	if len(f) <= startIdx {
		return "", fmt.Errorf("short /proc/%d/stat", pid)
	}
	return f[startIdx], nil
}

// selfProcStart is the current process's start-time, stamped onto a lease for the PID-reuse cross
// check. Best-effort ("" disables only the cross-check, never liveness).
func selfProcStart() string {
	st, _ := procStartTime(os.Getpid())
	return st
}

// --- driver-mode arbitration math -------------------------------------------------------------

// tokenHeldByShared reports whether any existing SHARED lease holds a token in tokens.
func tokenHeldByShared(ledger *spec.PreemptLedger, tokens []string) bool {
	for _, lz := range ledger.Leases {
		if lz.Shared && len(intersect(lz.Tokens, tokens)) > 0 {
			return true
		}
	}
	return false
}

// tokenClaimed reports whether any lease still claims a token overlapping toks.
func tokenClaimed(leases []spec.PreemptLease, toks []string) bool {
	for _, lz := range leases {
		if len(intersect(lz.Tokens, toks)) > 0 {
			return true
		}
	}
	return false
}

// desiredModeForToken computes the mode a token should be in given the active leases: vfio under
// any exclusive claim, nvidia while a shared claim remains, vfio when fully free.
func desiredModeForToken(leases []spec.PreemptLease, token string) string {
	hasShared := false
	for _, lz := range leases {
		if len(intersect(lz.Tokens, []string{token})) == 0 {
			continue
		}
		if lz.Shared {
			hasShared = true
		} else {
			return spec.GpuModeVfio
		}
	}
	if hasShared {
		return spec.GpuModeNvidia
	}
	return spec.GpuModeVfio
}

// attachPreemptedToSurvivor moves carried restore-obligations onto the first surviving lease
// overlapping each holder's token (the LAST release restores them).
func attachPreemptedToSurvivor(leases []spec.PreemptLease, carry []spec.PreemptedHolder) {
	for _, ph := range carry {
		for i := range leases {
			if len(intersect(leases[i].Tokens, ph.Holds)) > 0 {
				leases[i].Preempted = dedupePreempted(append(leases[i].Preempted, ph))
				break
			}
		}
	}
}

// dedupePreempted removes duplicate preempted holders by deploy name.
func dedupePreempted(in []spec.PreemptedHolder) []spec.PreemptedHolder {
	seen := map[string]bool{}
	var out []spec.PreemptedHolder
	for _, ph := range in {
		if ph.Addr.Name == "" || seen[ph.Addr.Name] {
			continue
		}
		seen[ph.Addr.Name] = true
		out = append(out, ph)
	}
	return out
}

// --- small set helpers ------------------------------------------------------------------------

func dedupeNonEmpty(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

func intersect(a, b []string) []string {
	set := map[string]bool{}
	for _, s := range a {
		set[s] = true
	}
	var out []string
	seen := map[string]bool{}
	for _, s := range b {
		if set[s] && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// errStr / errFromString are the reverse-channel string<->error convention (used by the arbiter
// Invoke replies + the host-seam decoders).
func errStr(err error) string {
	if err != nil {
		return err.Error()
	}
	return ""
}

func errFromString(s string) error {
	if s == "" {
		return nil
	}
	return errors.New(s)
}
