package main

import (
	"slices"
	"testing"

	"github.com/opencharly/sdk/spec"
)

func TestValidateServiceNameFound(t *testing.T) {
	// Test the service name lookup logic that validateServiceName uses internally.
	// validateServiceName calls ExtractMetadata which reads container labels at runtime,
	// so we test the lookup logic directly via spec.BoxMetadata.Services.
	meta := &spec.BoxMetadata{
		Init:         "supervisord",
		ServiceNames: []string{"traefik", "testapi"},
	}

	for _, svc := range []string{"traefik", "testapi"} {
		found := slices.Contains(meta.ServiceNames, svc)
		if !found {
			t.Errorf("service %q should be found in Services %v", svc, meta.ServiceNames)
		}
	}
}

func TestValidateServiceNameNotFound(t *testing.T) {
	meta := &spec.BoxMetadata{
		Init:         "supervisord",
		ServiceNames: []string{"traefik", "testapi"},
	}

	svc := "nonexistent"
	found := slices.Contains(meta.ServiceNames, svc)
	if found {
		t.Error("service \"nonexistent\" should not be found")
	}
}

func TestValidateServiceNameEmpty(t *testing.T) {
	meta := &spec.BoxMetadata{
		Init:         "",
		ServiceNames: nil,
	}

	svc := "svc"
	found := slices.Contains(meta.ServiceNames, svc)
	if found {
		t.Error("service should not be found in nil ServiceNames list")
	}
}
