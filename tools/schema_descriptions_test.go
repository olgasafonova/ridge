package tools

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"sort"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// connectForSchemas brings up a real in-memory session and returns the client
// side. Reading schemas off a live ListTools rather than off the Args structs
// is deliberate: the jsonschema tag is inferred by the SDK at registration, so
// only a session shows what a client actually receives. A malformed tag is a
// silent no-op that still compiles, and a struct-level check would not catch it.
func connectForSchemas(t *testing.T) *mcp.ClientSession {
	t.Helper()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := NewHandlerRegistry(logger)

	server := mcp.NewServer(&mcp.Implementation{Name: "ridge-test", Version: "0.0.0"}, nil)
	h.RegisterAll(server)

	clientTransport, serverTransport := mcp.NewInMemoryTransports()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = server.Run(ctx, serverTransport)
	}()

	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0.0.0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() {
		_ = session.Close()
		<-done
	})
	return session
}

// TestSchemaPropertiesAreDescribed asserts every input-schema property on every
// tool carries a description. An undescribed property forces a client to guess
// from the property name alone, which is the failure the go-mcptest protocol
// suite reports against this server.
func TestSchemaPropertiesAreDescribed(t *testing.T) {
	session := connectForSchemas(t)

	res, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(res.Tools) == 0 {
		t.Fatal("no tools registered")
	}

	total, missing := 0, 0
	byTool := map[string][]string{}

	for _, tool := range res.Tools {
		if tool.InputSchema == nil {
			continue
		}
		// InputSchema crosses the wire as raw JSON, so round-trip it rather
		// than reaching for a typed field that only exists server-side.
		raw, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatalf("%s: marshal input schema: %v", tool.Name, err)
		}
		var schema struct {
			Properties map[string]struct {
				Description string `json:"description"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Fatalf("%s: unmarshal input schema: %v", tool.Name, err)
		}
		for name, prop := range schema.Properties {
			total++
			if prop.Description == "" {
				missing++
				byTool[tool.Name] = append(byTool[tool.Name], name)
			}
		}
	}

	if missing > 0 {
		names := make([]string, 0, len(byTool))
		for n := range byTool {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			sort.Strings(byTool[n])
			t.Errorf("%s: %d undescribed %v", n, len(byTool[n]), byTool[n])
		}
		t.Errorf("TOTAL %d of %d properties undescribed across %d of %d tools",
			missing, total, len(byTool), len(res.Tools))
	}
}
