package main

import (
	"fmt"
	"testing"

	"github.com/opencharly/sdk/kit"
)

// TestFormatCLIError_ExtractsRefRegardlessOfWrapping proves FormatCLIError's marker search finds
// the image ref whether kit.ErrImageNotLocal is the WHOLE message (the direct ExtractMetadata
// caller shape) or wrapped by an outer layer (dispatchInProcCommand's "command %q: %w" — the
// compiled-in command-plugin dispatch every command word goes through, K3 reentry-class
// dissolution: candy/plugin-box's `labels` command was the first ErrImageNotLocal caller to run
// through this wrap, and it broke the old whole-message-prefix TrimPrefix).
func TestFormatCLIError_ExtractsRefRegardlessOfWrapping(t *testing.T) {
	const ref = "totally-nonexistent-image-xyz"
	want := fmt.Sprintf("image %q is not available locally.\nRun 'charly box pull %s' to fetch it first", ref, ref)

	direct := fmt.Errorf("%w: %s", kit.ErrImageNotLocal, ref)
	if got := FormatCLIError(direct).Error(); got != want {
		t.Errorf("direct (unwrapped) case: FormatCLIError(%v) = %q, want %q", direct, got, want)
	}

	wrapped := fmt.Errorf("command %q: %w", "labels", direct)
	if got := FormatCLIError(wrapped).Error(); got != want {
		t.Errorf("wrapped (command-dispatch) case: FormatCLIError(%v) = %q, want %q", wrapped, got, want)
	}

	other := fmt.Errorf("some unrelated failure")
	if got := FormatCLIError(other); got != other { //nolint:errorlint // intentional identity check: proving the passthrough returns the exact same value, not an equivalent-message copy
		t.Errorf("unrelated error must pass through unchanged, got %v", got)
	}

	if got := FormatCLIError(nil); got != nil {
		t.Errorf("FormatCLIError(nil) = %v, want nil", got)
	}
}
