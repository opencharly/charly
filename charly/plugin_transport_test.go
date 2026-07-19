package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestPluginClientLoggerKeepsNormalShutdownQuietAndRealWarningsVisible(t *testing.T) {
	var output bytes.Buffer
	logger := pluginClientLogger(&output)
	logger.Trace("waiting for stdio data")
	logger.Debug("received EOF, stopping recv loop", "err", errors.New("rpc error: EOF"))
	if output.Len() != 0 {
		t.Fatalf("routine plugin lifecycle diagnostics reached stderr: %q", output.String())
	}
	logger.Warn("plugin failed to stop cleanly", "err", errors.New("forced warning"))
	got := output.String()
	if !strings.Contains(got, "plugin failed to stop cleanly") || !strings.Contains(got, "forced warning") {
		t.Fatalf("real plugin warning was hidden: %q", got)
	}
}
