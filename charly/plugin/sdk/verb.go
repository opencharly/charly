package sdk

import (
	"encoding/json"
	"fmt"
	"strings"

	pb "github.com/opencharly/charly/charly/plugin/proto"
	"github.com/opencharly/charly/charly/spec"
)

// verb.go carries the shared helpers every OUT-OF-PROCESS check-verb provider uses: the
// {status,message} reply builder and the required-modifier check. They are the byte-identical
// boilerplate the EXEC-based verb plugins (dbus/record/wl) formerly each carried, hoisted here
// so the transport-invisible verb-serving surface has ONE home (R3).

// resultWire is the {status,message} wire form every out-of-process check verb returns (the
// host's pluginCheckResult). status ∈ "pass" | "fail" | "skip".
type resultWire struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}

// ResultJSON builds the InvokeReply an out-of-process check verb's Invoke returns — the SAME
// {status,message} shape every verb plugin (and ServeCheckVerb) emits (R3).
func ResultJSON(status, msg string) (*pb.InvokeReply, error) {
	j, err := json.Marshal(resultWire{Status: status, Message: msg})
	if err != nil {
		return nil, err
	}
	return &pb.InvokeReply{ResultJson: j}, nil
}

// CheckRequiredModifiers verifies every modifier a method requires is present on op, returning
// a "missing required modifier(s): …" error naming the absent ones. required maps a method
// name to its required modifier field names, and isZero reports whether a named modifier is
// absent (zero) on op — both are PER-VERB (the field set differs per plugin), so the caller
// supplies them while this shared loop owns the check + message (R3).
func CheckRequiredModifiers(method string, op *spec.Op, required map[string][]string, isZero func(op *spec.Op, name string) bool) error {
	var missing []string
	for _, f := range required[method] {
		if isZero(op, f) {
			missing = append(missing, f)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("missing required modifier(s): %s", strings.Join(missing, ", "))
}
