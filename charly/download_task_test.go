package main

import (
	"github.com/opencharly/sdk/spec"
	"strings"
	"testing"

	"github.com/opencharly/sdk/kit"
)

// Tests for renderDownloadScript — the host-side download executor.

func TestDownloadScriptPlain(t *testing.T) {
	task := &spec.Op{
		Download: "https://example.com/binary",
		To:       "/usr/local/bin/foo",
		Mode:     "0755",
		Extract:  "none",
	}
	out := kit.RenderDownloadScript(task, nil)
	if !strings.Contains(out, "curl -fL --retry 3 -o /usr/local/bin/foo") {
		t.Errorf("missing curl call: %s", out)
	}
	if !strings.Contains(out, "chmod 0755 /usr/local/bin/foo") {
		t.Errorf("missing chmod: %s", out)
	}
	if !strings.Contains(out, "install -d -m0755 /usr/local/bin") {
		t.Errorf("missing parent mkdir: %s", out)
	}
}

func TestDownloadScriptTarGzWithStrip(t *testing.T) {
	task := &spec.Op{
		Download:        "https://example.com/x.tar.gz",
		To:              "/usr/local/bin",
		Extract:         "tar.gz",
		StripComponents: 1,
	}
	out := kit.RenderDownloadScript(task, nil)
	if !strings.Contains(out, "tar -xzf") {
		t.Errorf("missing tar -xzf: %s", out)
	}
	if !strings.Contains(out, "--strip-components=1") {
		t.Errorf("missing --strip-components: %s", out)
	}
}

func TestDownloadScriptAutoDetectExtract(t *testing.T) {
	// Without explicit extract:, URL suffix drives the format.
	tests := map[string]string{
		"https://example.com/a.tar.gz":   "tar -xzf",
		"https://example.com/b.tgz":      "tar -xzf",
		"https://example.com/c.tar.xz":   "tar -xJf",
		"https://example.com/d.tar.zst":  "tar --zstd -xf",
		"https://example.com/e.zip":      "unzip",
		"https://example.com/install.sh": "chmod +x",
	}
	for url, sentinel := range tests {
		task := &spec.Op{Download: url, To: "/tmp/out"}
		out := kit.RenderDownloadScript(task, nil)
		if !strings.Contains(out, sentinel) {
			t.Errorf("URL %q: expected %q, got:\n%s", url, sentinel, out)
		}
	}
}

func TestDownloadScriptEnvVars(t *testing.T) {
	task := &spec.Op{
		Download: "https://example.com/install.sh",
		To:       "/tmp/x",
		Extract:  "sh",
		Env: map[string]string{
			"INSTALL_DIR": "/opt/foo",
			"API_KEY":     "secret",
		},
	}
	out := kit.RenderDownloadScript(task, nil)
	if !strings.Contains(out, "export API_KEY=secret") {
		t.Errorf("missing API_KEY export: %s", out)
	}
	if !strings.Contains(out, "export INSTALL_DIR=/opt/foo") {
		t.Errorf("missing INSTALL_DIR export: %s", out)
	}
}

func TestDownloadScriptInclude(t *testing.T) {
	task := &spec.Op{
		Download:       "https://example.com/bundle.tar.gz",
		To:             "/opt/bundle",
		Extract:        "tar.gz",
		ExtractInclude: []string{"bin/foo", "share/doc/foo"},
	}
	out := kit.RenderDownloadScript(task, nil)
	if !strings.Contains(out, "bin/foo") {
		t.Errorf("missing bin/foo path: %s", out)
	}
	if !strings.Contains(out, "share/doc/foo") {
		t.Errorf("missing share/doc path: %s", out)
	}
}

func TestDownloadScriptTmpCleanup(t *testing.T) {
	task := &spec.Op{Download: "https://example.com/x.tar.gz", To: "/tmp/out"}
	out := kit.RenderDownloadScript(task, nil)
	if !strings.Contains(out, `trap 'rm -rf "$ovtmp"' EXIT`) {
		t.Errorf("missing tmp cleanup trap: %s", out)
	}
}
