package main

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/olgasafonova/ridge/tools"
)

// connectToTestServer assembles the server exactly as main does — newServer plus
// the handler registry — and returns a connected client session. Driving the
// assembled server end to end is the point: asserting on cacheConfig alone would
// still pass if the middleware were never attached.
func connectToTestServer(t *testing.T) *mcp.ClientSession {
	t.Helper()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := newServer(logger)
	tools.NewHandlerRegistry(logger).RegisterAll(server)

	serverTransport, clientTransport := mcp.NewInMemoryTransports()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)

	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server.Connect: %v", err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	t.Cleanup(func() { _ = clientSession.Close() })

	return clientSession
}

// The regression this guards: the SDK leaves ttlMs at 0, which the MCP
// 2026-07-28 spec defines as immediately stale. Removing the
// AddReceivingMiddleware call in newServer must fail this test.
func TestToolsListAdvertisesCacheTTL(t *testing.T) {
	session := connectToTestServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	res, err := session.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	if len(res.Tools) == 0 {
		t.Fatal("tools/list returned no tools; the fixture is not exercising the real registry")
	}

	want := int(toolListTTL.Milliseconds())
	if got := res.GetTTLMs(); got != want {
		t.Errorf("tools/list ttlMs = %d, want %d", got, want)
	}

	// The SDK defaults this to "public"; assert it survived the middleware, since
	// a wrong scope on a cached result is a cross-user leak, not a performance nit.
	if got := res.GetCacheScope(); got != "public" {
		t.Errorf("tools/list cacheScope = %q, want %q", got, "public")
	}
}

// TestServerInstructionsCoverRegistries pins the server instructions to the
// live registries. The language and format lists are derived from
// tools.SupportedLanguages (the analyzers wired into NewHandlerRegistry) and
// tools.SupportedFormats (the renderFuncs dispatch map), so this test fails
// if the template wiring breaks or a registry stops feeding the instructions:
// adding a substrate or format without the instructions knowing is impossible
// while this passes.
func TestServerInstructionsCoverRegistries(t *testing.T) {
	instructions := buildServerInstructions()

	languages := tools.SupportedLanguages()
	if len(languages) == 0 {
		t.Fatal("tools.SupportedLanguages() returned no languages; the analyzer registry is empty")
	}
	for _, lang := range languages {
		if !strings.Contains(instructions, lang) {
			t.Errorf("server instructions missing supported language %q", lang)
		}
	}

	formats := tools.SupportedFormats()
	if len(formats) == 0 {
		t.Fatal("tools.SupportedFormats() returned no formats; the render dispatch map is empty")
	}
	for _, format := range formats {
		if !strings.Contains(instructions, format) {
			t.Errorf("server instructions missing output format %q", format)
		}
	}

	// A placeholder-count mismatch between the template and buildServerInstructions
	// would surface as a fmt error marker in the rendered text.
	if strings.Contains(instructions, "%!") {
		t.Errorf("server instructions contain an unrendered fmt placeholder:\n%s", instructions)
	}
}
