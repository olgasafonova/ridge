package render

import (
	"fmt"
	"strings"

	"github.com/olgasafonova/ridge/internal/model"
)

// Structurizr renders an ArchGraph as Structurizr DSL.
func Structurizr(graph *model.ArchGraph, opts Options) string {
	var sb strings.Builder

	title := opts.Title
	if title == "" {
		title = "Architecture"
	}

	vg := PrepareGraph(graph, opts)
	vg.TransitiveReduce()

	sb.WriteString("workspace {\n")
	sb.WriteString("    model {\n")

	externals, internals := splitExternalInternal(vg.Nodes)
	writeStructurizrExternals(&sb, externals)
	writeStructurizrSystem(&sb, title, internals)
	writeStructurizrRelationships(&sb, vg)

	sb.WriteString("    }\n") // end model
	writeStructurizrViews(&sb)
	sb.WriteString("}\n")
	return sb.String()
}

// writeStructurizrExternals renders softwareSystem blocks for external systems.
func writeStructurizrExternals(sb *strings.Builder, externals []*model.Node) {
	for _, n := range externals {
		fmt.Fprintf(sb, "        %s = softwareSystem \"%s\" {\n", SanitizeID(n.ID), n.Name)
		sb.WriteString("            tags \"External\"\n")
		sb.WriteString("        }\n")
	}
}

// writeStructurizrSystem renders the main software system with its containers.
func writeStructurizrSystem(sb *strings.Builder, title string, internals []*model.Node) {
	fmt.Fprintf(sb, "        system = softwareSystem \"%s\" {\n", title)
	for _, n := range internals {
		structurizrContainer(sb, n, SanitizeID(n.ID))
	}
	sb.WriteString("        }\n")
}

// writeStructurizrRelationships renders edge relationships.
func writeStructurizrRelationships(sb *strings.Builder, vg *VisibleGraph) {
	for _, e := range vg.Edges {
		label := EdgeLabel(e, vg.Names[e.Target])
		src, tgt := SanitizeID(e.Source), SanitizeID(e.Target)
		if label != "" {
			fmt.Fprintf(sb, "        %s -> %s \"%s\"\n", src, tgt, label)
		} else {
			fmt.Fprintf(sb, "        %s -> %s\n", src, tgt)
		}
	}
}

// writeStructurizrViews renders the standard view block.
func writeStructurizrViews(sb *strings.Builder) {
	sb.WriteString("    views {\n")
	sb.WriteString("        container system {\n")
	sb.WriteString("            include *\n")
	sb.WriteString("            autoLayout\n")
	sb.WriteString("        }\n")
	sb.WriteString("    }\n")
}

func structurizrContainer(sb *strings.Builder, n *model.Node, id string) {
	lang := n.Language
	if lang == "" {
		lang = ""
	}

	switch n.Type {
	case model.NodeDatabase:
		fmt.Fprintf(sb, "            %s = container \"%s\" \"\" \"%s\" {\n", id, n.Name, lang)
		sb.WriteString("                tags \"Database\"\n")
		sb.WriteString("            }\n")
	case model.NodeQueue:
		fmt.Fprintf(sb, "            %s = container \"%s\" \"\" \"%s\" {\n", id, n.Name, lang)
		sb.WriteString("                tags \"Queue\"\n")
		sb.WriteString("            }\n")
	case model.NodeCache:
		fmt.Fprintf(sb, "            %s = container \"%s\" \"\" \"%s\" {\n", id, n.Name, lang)
		sb.WriteString("                tags \"Cache\"\n")
		sb.WriteString("            }\n")
	default:
		fmt.Fprintf(sb, "            %s = container \"%s\" \"\" \"%s\"\n", id, n.Name, lang)
	}
}
