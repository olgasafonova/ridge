package scanner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/olgasafonova/ridge/internal/model"
)

func TestMaskSecrets(t *testing.T) {
	tests := []struct {
		name string
		line string
		want string
	}{
		{
			name: "api key assignment masked",
			line: `apiKey := "9f8e7d6c5b4a39281706f5e4d3c2b1a0"`,
			want: `apiKey := "[REDACTED]"`,
		},
		{
			name: "json password value masked",
			line: `  "password": "hunter2",`,
			want: `  "password": "[REDACTED]",`,
		},
		{
			name: "env style secret masked without quotes",
			line: `CLIENT_SECRET=s3cr3t-value-here`,
			want: `CLIENT_SECRET=[REDACTED]`,
		},
		{
			name: "github pat masked as bare string",
			line: `curl -H "X-Token: ghp_AbCdEfGhIjKlMnOpQrStUvWxYz012345"`,
			want: `curl -H "X-Token: [REDACTED]"`,
		},
		{
			name: "aws access key id masked",
			line: `aws_access_key_id = AKIAIOSFODNN7EXAMPLE`,
			want: `aws_access_key_id = [REDACTED]`,
		},
		{
			name: "bearer token masked keeping scheme",
			line: `req.Header.Set("Authorization", "Bearer eyJhbGciOiJIUzI1NiJ9.payload.sig")`,
			want: `req.Header.Set("Authorization", "Bearer [REDACTED]")`,
		},
		{
			name: "high entropy quoted literal in assignment masked",
			line: `signingKey = "QWxhZGRpbjpvcGVuIHNlc2FtZQ1234567890abcd"`,
			want: `signingKey = "[REDACTED]"`,
		},
		{
			name: "ordinary code passes through",
			line: `func extractSnippet(lines []string, startLine, count int) string {`,
			want: `func extractSnippet(lines []string, startLine, count int) string {`,
		},
		{
			name: "token read from environment passes through",
			line: `token := os.Getenv("RIDGE_TOKEN")`,
			want: `token := os.Getenv("RIDGE_TOKEN")`,
		},
		{
			name: "long url constant passes through",
			line: `docsURL = "https://github.com/olgasafonova/ridge/blob/main/README.md"`,
			want: `docsURL = "https://github.com/olgasafonova/ridge/blob/main/README.md"`,
		},
		{
			name: "long sentence constant passes through",
			line: `greeting = "the quick brown fox jumps over the lazy dog again"`,
			want: `greeting = "the quick brown fox jumps over the lazy dog again"`,
		},
		{
			name: "author field passes through",
			line: `author = "Olga Safonova"`,
			want: `author = "Olga Safonova"`,
		},
		{
			name: "nil token passes through",
			line: `token = nil`,
			want: `token = nil`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := maskSecrets(tt.line); got != tt.want {
				t.Errorf("maskSecrets(%q)\n got: %q\nwant: %q", tt.line, got, tt.want)
			}
		})
	}
}

// TestPopulateSamples_MasksSecrets covers the end-to-end path: a scanned file
// containing a hardcoded credential must not surface it via Node.Source.
func TestPopulateSamples_MasksSecrets(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "config.go")
	content := "package cfg\n\nconst apiKey = \"9f8e7d6c5b4a39281706f5e4d3c2b1a0\"\n"
	if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	g := model.NewGraph(dir)
	g.AddNode(&model.Node{ID: "cfg", Path: file})

	PopulateSamples(g, 5)

	got := g.GetNode("cfg").Source
	if strings.Contains(got, "9f8e7d6c5b4a39281706f5e4d3c2b1a0") {
		t.Fatalf("hardcoded key leaked into Node.Source: %q", got)
	}
	if !strings.Contains(got, redactedPlaceholder) {
		t.Fatalf("expected %s marker in Node.Source, got: %q", redactedPlaceholder, got)
	}
}
