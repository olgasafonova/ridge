package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/olgasafonova/ridge/tools"
)

// scanFixture is a small project with one production file and one helper
// under tests/.
func scanFixture(t *testing.T) string {
	t.Helper()
	// EvalSymlinks: on macOS the temp dir sits behind a /var -> /private/var link.
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"src/app.ts":             "import { helper } from './helper';\nexport const app = helper;\n",
		"src/helper.ts":          "export const helper = 1;\n",
		"tests/builders/make.ts": "export const make = () => ({});\n",
		"docs/notes.md":          "# Notes\n",
	}
	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func runForTest(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = runCommand(context.Background(), args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func nodeNames(t *testing.T, data []byte) map[string]bool {
	t.Helper()
	var result tools.ArchScanResult
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("output is not scan JSON: %v\n%s", err, data)
	}
	if result.NodeCount != len(result.Nodes) {
		t.Fatalf("node_count %d but %d nodes written", result.NodeCount, len(result.Nodes))
	}
	names := make(map[string]bool)
	for _, n := range result.Nodes {
		names[n.Name] = true
	}
	return names
}

func TestScanCommand_WritesJSONToStdout(t *testing.T) {
	code, stdout, stderr := runForTest(t, "scan", scanFixture(t))
	if code != exitOK {
		t.Fatalf("exit %d, stderr: %s", code, stderr)
	}
	names := nodeNames(t, []byte(stdout))
	if !names["app"] || !names["helper"] {
		t.Errorf("missing production modules in %v", names)
	}
	if names["make"] {
		t.Errorf("tests/ helper is in the default scan: %v", names)
	}
}

func TestScanCommand_FlagsReachTheScan(t *testing.T) {
	dir := scanFixture(t)
	out := filepath.Join(t.TempDir(), "scan.json")
	code, stdout, stderr := runForTest(t, "scan", "-include-tests", "-skip-glob", "**/*.md", "-skip-dir", "nothing", "-out", out, dir)
	if code != exitOK {
		t.Fatalf("exit %d, stderr: %s", code, stderr)
	}
	if !strings.HasPrefix(stdout, "wrote ") || !strings.Contains(stdout, out) {
		t.Errorf("stdout = %q, want a line naming the file", stdout)
	}
	data, err := os.ReadFile(out) // #nosec G304 -- test file under t.TempDir
	if err != nil {
		t.Fatal(err)
	}
	names := nodeNames(t, data)
	if !names["make"] {
		t.Errorf("-include-tests did not bring back the tests/ helper: %v", names)
	}
	if names["notes"] {
		t.Errorf("-skip-glob **/*.md left the note in: %v", names)
	}
}

func TestScanCommand_Failures(t *testing.T) {
	dir := scanFixture(t)
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStderr string
	}{
		{"unknown command", []string{"render"}, exitUsage, `unknown command "render"`},
		{"unknown flag", []string{"scan", "-nope", dir}, exitUsage, "flag provided but not defined"},
		{"no path", []string{"scan"}, exitUsage, "scan needs a path or -repo"},
		{"two paths", []string{"scan", dir, dir}, exitUsage, "scan takes one path"},
		{"missing folder", []string{"scan", filepath.Join(dir, "missing")}, exitError, "scan failed"},
		{"unwritable output", []string{"scan", "-out", filepath.Join(dir, "no", "such", "dir", "x.json"), dir}, exitError, "writing "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, stdout, stderr := runForTest(t, tt.args...)
			if code != tt.wantCode {
				t.Errorf("exit %d, want %d", code, tt.wantCode)
			}
			if !strings.Contains(stderr, tt.wantStderr) {
				t.Errorf("stderr = %q, want it to contain %q", stderr, tt.wantStderr)
			}
			if stdout != "" {
				t.Errorf("stdout = %q, want nothing on failure", stdout)
			}
		})
	}
}

func TestScanCommand_Help(t *testing.T) {
	code, stdout, stderr := runForTest(t, "scan", "-h")
	if code != exitOK {
		t.Errorf("exit %d, want %d", code, exitOK)
	}
	if stdout != "" || !strings.Contains(stderr, "Usage: code-to-arch scan") || !strings.Contains(stderr, "-skip-glob") {
		t.Errorf("help not printed to stderr:\nstdout=%q\nstderr=%q", stdout, stderr)
	}
}

// failingWriter stands for a closed pipe.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, os.ErrClosed }

func TestScanCommand_StdoutWriteFailure(t *testing.T) {
	var stderr bytes.Buffer
	code := runCommand(context.Background(), []string{"scan", scanFixture(t)}, failingWriter{}, &stderr)
	if code != exitError || !strings.Contains(stderr.String(), "writing result") {
		t.Fatalf("exit %d, stderr %q; want exit %d and a write error", code, stderr.String(), exitError)
	}
}

func TestScanCommand_SaysWhenALimitCutTheScan(t *testing.T) {
	out := filepath.Join(t.TempDir(), "scan.json")
	code, stdout, stderr := runForTest(t, "scan", "-max-files", "1", "-out", out, scanFixture(t))
	if code != exitOK {
		t.Fatalf("exit %d, stderr: %s", code, stderr)
	}
	if !strings.Contains(stdout, "truncated") {
		t.Fatalf("stdout = %q, want it to say the scan was truncated", stdout)
	}
}
