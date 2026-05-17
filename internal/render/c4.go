package render

import (
	"fmt"
	"strings"

	"github.com/olgasafonova/ridge/internal/model"
)

// C4 renders an ArchGraph using C4-PlantUML notation.
func C4(graph *model.ArchGraph, opts Options) string {
	var sb strings.Builder

	title := opts.Title
	if title == "" {
		title = "Architecture"
	}

	vg := PrepareGraph(graph, opts)
	vg.TransitiveReduce()

	sb.WriteString("@startuml\n")
	sb.WriteString("!include <C4/C4_Container>\n\n")
	fmt.Fprintf(&sb, "title %s\n\n", title)

	externals, internals := splitExternalInternal(vg.Nodes)
	writeC4Externals(&sb, externals)
	writeC4Internals(&sb, internals)
	writeC4Relationships(&sb, vg)

	sb.WriteString("\n@enduml\n")
	return sb.String()
}

// splitExternalInternal partitions nodes into external-API and everything else.
func splitExternalInternal(nodes []*model.Node) (externals, internals []*model.Node) {
	for _, n := range nodes {
		if n.Type == model.NodeExternalAPI {
			externals = append(externals, n)
		} else {
			internals = append(internals, n)
		}
	}
	return externals, internals
}

// writeC4Externals renders System_Ext blocks for external systems.
func writeC4Externals(sb *strings.Builder, externals []*model.Node) {
	for _, n := range externals {
		fmt.Fprintf(sb, "System_Ext(%s, \"%s\")\n", SanitizeID(n.ID), n.Name)
	}
	if len(externals) > 0 {
		sb.WriteString("\n")
	}
}

// writeC4Internals renders the system boundary with internal containers.
func writeC4Internals(sb *strings.Builder, internals []*model.Node) {
	if len(internals) == 0 {
		return
	}
	sb.WriteString("System_Boundary(system, \"System\") {\n")
	for _, n := range internals {
		c4Element(sb, n, SanitizeID(n.ID))
	}
	sb.WriteString("}\n\n")
}

// writeC4Relationships renders Rel() lines for each edge.
func writeC4Relationships(sb *strings.Builder, vg *VisibleGraph) {
	for _, e := range vg.Edges {
		label := EdgeLabel(e, vg.Names[e.Target])
		fmt.Fprintf(sb, "Rel(%s, %s, \"%s\")\n",
			SanitizeID(e.Source), SanitizeID(e.Target), label)
	}
}

func c4Element(sb *strings.Builder, n *model.Node, id string) {
	lang := n.Language
	if lang == "" {
		lang = ""
	}

	switch n.Type {
	case model.NodeDatabase:
		fmt.Fprintf(sb, "    ContainerDb(%s, \"%s\", \"%s\", \"Data store\")\n", id, n.Name, lang)
	case model.NodeQueue:
		fmt.Fprintf(sb, "    Container(%s, \"%s\", \"%s\", \"Message queue\") <<queue>>\n", id, n.Name, lang)
	case model.NodeCache:
		fmt.Fprintf(sb, "    Container(%s, \"%s\", \"%s\", \"Cache\") <<cache>>\n", id, n.Name, lang)
	case model.NodeService, model.NodeModule:
		fmt.Fprintf(sb, "    Container(%s, \"%s\", \"%s\", \"Service\")\n", id, n.Name, lang)
	case model.NodePackage:
		fmt.Fprintf(sb, "    Component(%s, \"%s\", \"%s\", \"Package\")\n", id, n.Name, lang)
	case model.NodeEndpoint:
		fmt.Fprintf(sb, "    Component(%s, \"%s\", \"%s\", \"Endpoint\")\n", id, n.Name, lang)
	default:
		fmt.Fprintf(sb, "    Container(%s, \"%s\", \"%s\", \"\")\n", id, n.Name, lang)
	}
}
