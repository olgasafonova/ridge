package tracing

import (
	"context"
	"testing"
)

// TestSetup_EnabledWithStdout guards the semconv pin against the OTel SDK's
// bundled resource schema. resource.Merge refuses to combine resources with
// conflicting Schema URLs, so an SDK minor bump that advances the bundled
// schema breaks Setup until the semconv import here is bumped to match.
func TestSetup_EnabledWithStdout(t *testing.T) {
	cfg := Config{
		ServiceName:    "test-service",
		ServiceVersion: "1.0.0",
		Environment:    "test",
		Enabled:        true,
		OTLPEndpoint:   "", // Empty means stdout exporter
		SampleRate:     1.0,
	}

	shutdown, err := Setup(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}
	defer func() { _ = shutdown(context.Background()) }()

	if tracer := Tracer(); tracer == nil {
		t.Error("Expected tracer to be non-nil")
	}
}

func TestSetup_DifferentSampleRates(t *testing.T) {
	tests := []struct {
		name       string
		sampleRate float64
	}{
		{"always sample", 1.0},
		{"never sample", 0.0},
		{"ratio sample", 0.5},
		{"above 1.0", 1.5},  // Should still work, treated as always
		{"below 0.0", -0.5}, // Should still work, treated as never
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Config{
				ServiceName:    "test-service",
				ServiceVersion: "1.0.0",
				Environment:    "test",
				Enabled:        true,
				SampleRate:     tt.sampleRate,
			}

			shutdown, err := Setup(context.Background(), cfg)
			if err != nil {
				t.Fatalf("Setup failed: %v", err)
			}
			_ = shutdown(context.Background())
		})
	}
}

func TestSetup_Disabled(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = false

	shutdown, err := Setup(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}
	if shutdown == nil {
		t.Fatal("Expected non-nil shutdown func when tracing is disabled")
	}
	_ = shutdown(context.Background())
}
