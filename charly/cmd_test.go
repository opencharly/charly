package main

import "testing"

func TestCmdResolveContainerFails(t *testing.T) {
	cmd := CmdCmd{
		Box:     "nonexistent-image",
		Command: "echo hello",
	}
	err := cmd.Run()
	if err == nil {
		t.Error("expected error when container is not running")
	}
}

func TestCmdWithInstance(t *testing.T) {
	cmd := CmdCmd{
		Box:      "nonexistent-image",
		Command:  "echo hello",
		Instance: "test-instance",
	}
	err := cmd.Run()
	if err == nil {
		t.Error("expected error when container is not running")
	}
}
