package tools

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/olgasafonova/ridge/internal/registry"
	"github.com/olgasafonova/ridge/internal/safepath"
)

// =============================================================================
// arch_registry_add
// =============================================================================

type ArchRegistryAddArgs struct {
	Path  string `json:"path" jsonschema:"Absolute filesystem path to the codebase directory to register. Must exist and be a directory."`
	Alias string `json:"alias,omitempty" jsonschema:"Optional short name used to refer to this repo in later tool calls. Defaults to the directory basename when omitted. Must be unique across registered repos."`
}

type ArchRegistryAddResult struct {
	Alias   string `json:"alias"`
	Path    string `json:"path"`
	Summary string `json:"summary"`
}

func (h *HandlerRegistry) archRegistryAdd(_ context.Context, args ArchRegistryAddArgs) (*ArchRegistryAddResult, error) {
	if h.repoRegistry == nil {
		return nil, fmt.Errorf("repo registry not available")
	}
	if err := safepath.ValidateScanPath(args.Path); err != nil {
		return nil, err
	}

	absPath, err := filepath.Abs(args.Path)
	if err != nil {
		return nil, fmt.Errorf("resolving path: %w", err)
	}

	alias := args.Alias
	if alias == "" {
		alias = filepath.Base(absPath)
	}
	// Validate at the handler entry so the error message reaches the agent
	// before any registry-internal step runs. registry.Add validates again as
	// defense in depth.
	if err := registry.ValidateAlias(alias); err != nil {
		return nil, fmt.Errorf("invalid alias: %w", err)
	}

	if err := h.repoRegistry.Add(alias, absPath); err != nil {
		return nil, err
	}
	if err := h.repoRegistry.Save(); err != nil {
		return nil, fmt.Errorf("saving registry: %w", err)
	}

	return &ArchRegistryAddResult{
		Alias:   alias,
		Path:    absPath,
		Summary: fmt.Sprintf("Registered %q -> %s", alias, absPath),
	}, nil
}

// =============================================================================
// arch_registry_remove
// =============================================================================

type ArchRegistryRemoveArgs struct {
	Alias string `json:"alias" jsonschema:"Short name of a previously registered repo to remove. Run arch_registry_list to see available aliases. Also deletes any persisted incremental scan state for the alias."`
}

type ArchRegistryRemoveResult struct {
	Alias   string `json:"alias"`
	Summary string `json:"summary"`
}

func (h *HandlerRegistry) archRegistryRemove(_ context.Context, args ArchRegistryRemoveArgs) (*ArchRegistryRemoveResult, error) {
	if h.repoRegistry == nil {
		return nil, fmt.Errorf("repo registry not available")
	}
	if args.Alias == "" {
		return nil, fmt.Errorf("alias is required")
	}

	if err := h.repoRegistry.Remove(args.Alias); err != nil {
		return nil, err
	}
	if err := h.repoRegistry.Save(); err != nil {
		return nil, fmt.Errorf("saving registry: %w", err)
	}

	return &ArchRegistryRemoveResult{
		Alias:   args.Alias,
		Summary: fmt.Sprintf("Removed %q from registry", args.Alias),
	}, nil
}

// =============================================================================
// arch_registry_list
// =============================================================================

type ArchRegistryListArgs struct{}

type ArchRegistryListResult struct {
	Repos   []registry.RepoEntry `json:"repos"`
	Summary string               `json:"summary"`
}

func (h *HandlerRegistry) archRegistryList(_ context.Context, _ ArchRegistryListArgs) (*ArchRegistryListResult, error) {
	if h.repoRegistry == nil {
		return &ArchRegistryListResult{
			Repos:   []registry.RepoEntry{},
			Summary: "No repos registered (registry not available)",
		}, nil
	}

	entries := h.repoRegistry.List()
	if entries == nil {
		entries = []registry.RepoEntry{}
	}

	return &ArchRegistryListResult{
		Repos:   entries,
		Summary: fmt.Sprintf("%d repos registered", len(entries)),
	}, nil
}
