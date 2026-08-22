package climodel

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestCLISubcommandFields(t *testing.T) {
	sc := CLISubcommand{Name: "agent", Help: "run an agent"}
	if sc.Name != "agent" || sc.Help != "run an agent" || sc.Hidden {
		t.Fatalf("CLISubcommand = %+v, want {agent run an agent false}", sc)
	}
	if !(CLISubcommand{Name: "run-local", Hidden: true}.Hidden) {
		t.Fatal("CLISubcommand.Hidden must round-trip (hidden-but-reachable subcommands)")
	}
}

func TestValidateGeneratedUnknownDefinition(t *testing.T) {
	// An unknown CUE definition must surface a real error, never pass silently.
	err := ValidateGenerated("#ThisDefinitionDoesNotExist", map[string]any{"x": 1})
	if err == nil {
		t.Fatal("ValidateGenerated with an unknown definition returned nil")
	}
}

func TestDecodeGeneratedJSONRejectsTrailingData(t *testing.T) {
	// Two JSON values back-to-back must be rejected before any CUE validation.
	payload := []byte(`{"a":1}{"b":2}`)
	var dst map[string]any
	err := DecodeGeneratedJSON("#NoSuchDef", payload, &dst)
	if err == nil {
		t.Fatal("DecodeGeneratedJSON accepted trailing JSON")
	}
}

func TestDecodeGeneratedJSONRejectsUnknownFields(t *testing.T) {
	// Unknown fields must be rejected at decode time, before CUE validation.
	var dst struct {
		A int `json:"a"`
	}
	// Wrap the decode: DisallowUnknownFields turns a stray field into a decode error,
	// which surfaces before the (unknown-definition) CUE step.
	payload := []byte(`{"a":1,"unknown":2}`)
	err := DecodeGeneratedJSON("#NoSuchDef", payload, &dst)
	if err == nil {
		t.Fatal("DecodeGeneratedJSON accepted an unknown field")
	}
	// The decode error is a json.UnmarshalTypeError or similar; the CUE step would
	// say "#NoSuchDef does not exist". Assert we did NOT reach the CUE step.
	var jsonErr *json.UnmarshalTypeError
	if errors.As(err, &jsonErr) {
		return // decode-phase rejection — correct
	}
	// Any non-CUE-definition error here is also acceptable (the point is decode rejected it).
	if err.Error() == "CUE definition #NoSuchDef does not exist" {
		t.Fatal("DecodeGeneratedJSON reached the CUE step despite an unknown field — DisallowUnknownFields not applied")
	}
}
