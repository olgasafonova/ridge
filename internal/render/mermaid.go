// Package render provides diagram output renderers.
package render

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/olgasafonova/ridge/internal/model"
)

// Format enumerates supported output formats.
type Format string

const (
	FormatMermaid     Format = "mermaid"
	FormatPlantUML    Format = "plantuml"
	FormatC4          Format = "c4"
	FormatStructurizr Format = "structurizr"
	FormatJSON        Format = "json"
	FormatDrawIO      Format = "drawio"
	FormatExcalidraw  Format = "excalidraw"
	FormatHTML        Format = "html"
	FormatForceGraph  Format = "forcegraph"
)

// ViewLevel controls the level of detail in rendered output.
type ViewLevel string

const (
	ViewSystem    ViewLevel = "system"    // High-level service overview
	ViewContainer ViewLevel = "container" // Services + databases + queues
	ViewComponent ViewLevel = "component" // Internal packages and modules
)

// Theme controls diagram colors. All colors are derived from BG (background)
// and FG (foreground) using percentage mixing, inspired by beautiful-mermaid's
// two-color derivation. If both BG and FG are empty, no theme is applied.
type Theme struct {
	BG string // Background hex color, e.g. "#ffffff"
	FG string // Foreground hex color, e.g. "#1e293b"
}

// Options controls rendering behavior.
type Options struct {
	Format         Format
	ViewLevel      ViewLevel
	Title          string
	Direction      string  // TB, LR, RL, BT (Mermaid direction)
	Theme          Theme   // Optional two-color theme for Mermaid output
	PruneThreshold float64 // 0 = disabled; 0.5 = prune nodes with fan-in > 50% of sources
	MinDegree      int     // 0 = disabled; otherwise drop nodes whose total degree (in + out) is below this threshold
}

// DefaultOptions returns sensible rendering defaults.
func DefaultOptions() Options {
	return Options{
		Format:    FormatMermaid,
		ViewLevel: ViewContainer,
		Direction: "TB",
	}
}

// Mermaid renders an ArchGraph as a Mermaid diagram.
func Mermaid(graph *model.ArchGraph, opts Options) string {
	var sb strings.Builder

	direction := opts.Direction
	if direction == "" {
		direction = "TB"
	}

	title := opts.Title
	if title == "" {
		title = "Architecture"
	}

	// Inject theme frontmatter if colors are provided.
	if themeInit := mermaidThemeInit(opts.Theme); themeInit != "" {
		sb.WriteString(themeInit)
	}

	fmt.Fprintf(&sb, "---\ntitle: %s\n---\n", title)
	fmt.Fprintf(&sb, "graph %s\n", direction)

	vg := PrepareGraph(graph, opts)
	vg.TransitiveReduce()

	// Render nodes grouped by type
	renderNodeGroup(&sb, vg.Nodes, model.NodeService, "Services")
	renderNodeGroup(&sb, vg.Nodes, model.NodeModule, "Modules")
	renderNodeGroup(&sb, vg.Nodes, model.NodePackage, "Packages")
	renderNodeGroup(&sb, vg.Nodes, model.NodeDatabase, "Data Stores")
	renderNodeGroup(&sb, vg.Nodes, model.NodeQueue, "Message Queues")
	renderNodeGroup(&sb, vg.Nodes, model.NodeCache, "Caches")
	renderNodeGroup(&sb, vg.Nodes, model.NodeExternalAPI, "External APIs")
	renderNodeGroup(&sb, vg.Nodes, model.NodeEndpoint, "Endpoints")
	renderNodeGroup(&sb, vg.Nodes, model.NodeNote, "Notes")

	// Render edges between visible nodes
	for _, e := range vg.Edges {
		label := EdgeLabel(e, vg.Names[e.Target])
		if label != "" {
			fmt.Fprintf(&sb, "    %s -->|%s| %s\n",
				SanitizeID(e.Source), label, SanitizeID(e.Target))
		} else {
			fmt.Fprintf(&sb, "    %s --> %s\n",
				SanitizeID(e.Source), SanitizeID(e.Target))
		}
	}

	return sb.String()
}

func renderNodeGroup(sb *strings.Builder, nodes []*model.Node, nodeType model.NodeType, groupLabel string) {
	var group []*model.Node
	for _, n := range nodes {
		if n.Type == nodeType {
			group = append(group, n)
		}
	}
	if len(group) == 0 {
		return
	}

	fmt.Fprintf(sb, "    subgraph %s\n", groupLabel)
	for _, n := range group {
		id := SanitizeID(n.ID)
		shape := mermaidShape(n.Type)
		fmt.Fprintf(sb, "        %s%s%s%s\n", id, shape.open, mermaidLabel(n.Name), shape.close)
	}
	sb.WriteString("    end\n")
}

// mermaidLabel quotes a node label when it contains characters that confuse
// Mermaid's shape parser (parens, brackets, braces). Embedded double quotes
// are converted to the &quot; HTML entity, which Mermaid renders correctly.
func mermaidLabel(name string) string {
	if !strings.ContainsAny(name, `()[]{}"`) {
		return name
	}
	escaped := strings.ReplaceAll(name, `"`, "&quot;")
	return `"` + escaped + `"`
}

type shapeDelimiters struct {
	open, close string
}

func mermaidShape(t model.NodeType) shapeDelimiters {
	switch t {
	case model.NodeDatabase:
		return shapeDelimiters{"[(", ")]"}
	case model.NodeQueue:
		return shapeDelimiters{"[[", "]]"}
	case model.NodeCache:
		return shapeDelimiters{"((", "))"}
	case model.NodeExternalAPI:
		return shapeDelimiters{">", "]"}
	case model.NodeEndpoint:
		return shapeDelimiters{"{{", "}}"}
	case model.NodeNote:
		return shapeDelimiters{"([", "])"}
	default:
		return shapeDelimiters{"[", "]"}
	}
}

// mermaidThemeInit generates a %%{init: ...}%% directive that sets Mermaid's
// base theme variables. Colors are derived from two inputs (BG and FG) using
// linear interpolation at fixed percentages, inspired by beautiful-mermaid's
// two-color derivation system.
//
// Derivation table:
//   - primaryColor (node fill):      3% FG into BG
//   - primaryTextColor:              FG at 100%
//   - primaryBorderColor:            30% FG into BG
//   - lineColor (connectors):        30% FG into BG
//   - secondaryColor (alt nodes):    6% FG into BG
//   - tertiaryColor (subgraph bg):   2% FG into BG
//   - mainBkg:                       3% FG into BG
//   - nodeBorder:                    30% FG into BG
//   - clusterBkg:                    2% FG into BG
//   - titleColor:                    FG at 100%
func mermaidThemeInit(t Theme) string {
	if t.BG == "" || t.FG == "" {
		return ""
	}

	bgR, bgG, bgB, ok1 := parseHex(t.BG)
	fgR, fgG, fgB, ok2 := parseHex(t.FG)
	if !ok1 || !ok2 {
		return ""
	}

	mix := func(pct float64) string {
		r := uint8(float64(bgR) + pct*(float64(fgR)-float64(bgR)))
		g := uint8(float64(bgG) + pct*(float64(fgG)-float64(bgG)))
		b := uint8(float64(bgB) + pct*(float64(fgB)-float64(bgB)))
		return fmt.Sprintf("#%02x%02x%02x", r, g, b)
	}

	return fmt.Sprintf(
		"%%%%{init: {'theme': 'base', 'themeVariables': {"+
			"'primaryColor': '%s', "+
			"'primaryTextColor': '%s', "+
			"'primaryBorderColor': '%s', "+
			"'lineColor': '%s', "+
			"'secondaryColor': '%s', "+
			"'tertiaryColor': '%s', "+
			"'mainBkg': '%s', "+
			"'nodeBorder': '%s', "+
			"'clusterBkg': '%s', "+
			"'titleColor': '%s'"+
			"}}}%%%%\n",
		mix(0.03), // primaryColor
		t.FG,      // primaryTextColor
		mix(0.30), // primaryBorderColor
		mix(0.30), // lineColor
		mix(0.06), // secondaryColor
		mix(0.02), // tertiaryColor
		mix(0.03), // mainBkg
		mix(0.30), // nodeBorder
		mix(0.02), // clusterBkg
		t.FG,      // titleColor
	)
}

// parseHex parses a hex color string (#RGB, #RRGGBB) into r, g, b components.
func parseHex(hex string) (r, g, b uint8, ok bool) {
	hex = strings.TrimPrefix(hex, "#")
	switch len(hex) {
	case 3:
		// Expand shorthand: #RGB → #RRGGBB
		hex = string([]byte{hex[0], hex[0], hex[1], hex[1], hex[2], hex[2]})
	case 6:
		// Already full form
	default:
		return 0, 0, 0, false
	}

	var rgb [3]uint8
	for i := range rgb {
		v, err := strconv.ParseUint(hex[2*i:2*i+2], 16, 8)
		if err != nil {
			return 0, 0, 0, false
		}
		rgb[i] = uint8(v)
	}
	return rgb[0], rgb[1], rgb[2], true
}
