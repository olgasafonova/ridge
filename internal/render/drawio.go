package render

import (
	"fmt"
	"strings"

	"github.com/olgasafonova/ridge/internal/model"
)

const (
	drawIOCellW    = 200
	drawIOCellH    = 60
	drawIOGapX     = 60
	drawIOGapY     = 120
	drawIOMarginX  = 40
	drawIOMarginY  = 40
	drawIOGroupPad = 16
)

// drawIOBounds tracks the rectangular bounds of a node-type cluster.
type drawIOBounds struct {
	minX, minY, maxX, maxY int
	label                  string
}

// DrawIO renders an ArchGraph as draw.io (mxGraphModel) XML.
// Nodes are arranged in topological layers: root packages at the top,
// leaf dependencies at the bottom.
func DrawIO(graph *model.ArchGraph, opts Options) string {
	vg := PrepareGraph(graph, opts)
	vg.TransitiveReduce()

	depths, maxDepth := ComputeNodeDepths(vg)
	layers := LayerByDepth(vg.Nodes, depths, maxDepth)
	BarycenterOrder(layers, vg.Edges)
	maxWidth := drawIOLayoutWidth(layers)

	var sb strings.Builder
	sb.WriteString("<mxGraphModel>\n  <root>\n    <mxCell id=\"0\"/>\n    <mxCell id=\"1\" parent=\"0\"/>\n")
	groupBounds := drawIOEmitNodes(&sb, layers, maxDepth, maxWidth)
	drawIOEmitGroupFrames(&sb, groupBounds)
	drawIOEmitEdges(&sb, vg)
	sb.WriteString("  </root>\n</mxGraphModel>\n")
	return sb.String()
}

// drawIOLayoutWidth returns the canvas width needed for the widest layer.
func drawIOLayoutWidth(layers [][]*model.Node) int {
	maxCount := 0
	for _, nodes := range layers {
		if len(nodes) > maxCount {
			maxCount = len(nodes)
		}
	}
	if maxCount == 0 {
		maxCount = 1
	}
	return maxCount*drawIOCellW + (maxCount-1)*drawIOGapX
}

// drawIOEmitNodes positions each node in its depth row, emits the cell XML,
// and returns the bounds of every NodeType cluster.
func drawIOEmitNodes(sb *strings.Builder, layers [][]*model.Node, maxDepth, maxWidth int) map[model.NodeType]*drawIOBounds {
	groupBounds := make(map[model.NodeType]*drawIOBounds)
	for d := maxDepth; d >= 0; d-- {
		emitDrawIOLayer(sb, layers[d], maxDepth-d, maxWidth, groupBounds)
	}
	return groupBounds
}

// emitDrawIOLayer writes one row of cells and updates the per-type bounds.
func emitDrawIOLayer(sb *strings.Builder, nodes []*model.Node, row, maxWidth int, groupBounds map[model.NodeType]*drawIOBounds) {
	layerWidth := len(nodes)*drawIOCellW + (len(nodes)-1)*drawIOGapX
	offsetX := (maxWidth - layerWidth) / 2
	y := drawIOMarginY + row*(drawIOCellH+drawIOGapY)

	for j, n := range nodes {
		x := drawIOMarginX + offsetX + j*(drawIOCellW+drawIOGapX)
		drawIOEmitCell(sb, n, x, y)
		expandDrawIOBounds(groupBounds, n.Type, x, y)
	}
}

// drawIOEmitCell writes a single mxCell vertex.
func drawIOEmitCell(sb *strings.Builder, n *model.Node, x, y int) {
	fmt.Fprintf(sb, "    <mxCell id=\"%s\" value=\"%s\" style=\"%s\" vertex=\"1\" parent=\"1\">\n",
		SanitizeID(n.ID), xmlEscape(n.Name), drawIOStyle(n.Type))
	fmt.Fprintf(sb, "      <mxGeometry x=\"%d\" y=\"%d\" width=\"%d\" height=\"%d\" as=\"geometry\"/>\n",
		x, y, drawIOCellW, drawIOCellH)
	sb.WriteString("    </mxCell>\n")
}

// expandDrawIOBounds widens (or initializes) the bounds for a NodeType cluster
// to enclose a cell at (x, y).
func expandDrawIOBounds(groupBounds map[model.NodeType]*drawIOBounds, t model.NodeType, x, y int) {
	b := groupBounds[t]
	if b == nil {
		groupBounds[t] = &drawIOBounds{
			minX:  x,
			minY:  y,
			maxX:  x + drawIOCellW,
			maxY:  y + drawIOCellH,
			label: drawIOGroupLabel(t),
		}
		return
	}
	if x < b.minX {
		b.minX = x
	}
	if y < b.minY {
		b.minY = y
	}
	if x+drawIOCellW > b.maxX {
		b.maxX = x + drawIOCellW
	}
	if y+drawIOCellH > b.maxY {
		b.maxY = y + drawIOCellH
	}
}

// drawIOEmitGroupFrames writes one translucent backdrop rectangle per cluster.
func drawIOEmitGroupFrames(sb *strings.Builder, groupBounds map[model.NodeType]*drawIOBounds) {
	for nt, b := range groupBounds {
		emitDrawIOGroupFrame(sb, nt, b)
	}
}

func emitDrawIOGroupFrame(sb *strings.Builder, nt model.NodeType, b *drawIOBounds) {
	gx := b.minX - drawIOGroupPad
	gy := b.minY - drawIOGroupPad - 20 // extra space for label
	gw := b.maxX - b.minX + 2*drawIOGroupPad
	gh := b.maxY - b.minY + 2*drawIOGroupPad + 20
	gID := fmt.Sprintf("group_%s", SanitizeID(string(nt)))
	color := drawIOGroupColor(nt)

	fmt.Fprintf(sb, "    <mxCell id=\"%s\" value=\"%s\" style=\"rounded=1;whiteSpace=wrap;html=1;fillColor=%s;strokeColor=%s;opacity=30;verticalAlign=top;fontStyle=1;fontSize=12;\" vertex=\"1\" parent=\"1\">\n",
		gID, xmlEscape(b.label), color, color)
	fmt.Fprintf(sb, "      <mxGeometry x=\"%d\" y=\"%d\" width=\"%d\" height=\"%d\" as=\"geometry\"/>\n",
		gx, gy, gw, gh)
	sb.WriteString("    </mxCell>\n")
}

// drawIOEmitEdges writes one mxCell edge per visible edge.
func drawIOEmitEdges(sb *strings.Builder, vg *VisibleGraph) {
	for i, e := range vg.Edges {
		emitDrawIOEdge(sb, i, e, vg.Names[e.Target])
	}
}

func emitDrawIOEdge(sb *strings.Builder, index int, e *model.Edge, targetName string) {
	label := EdgeLabel(e, targetName)
	edgeID := fmt.Sprintf("edge_%d", index)
	fmt.Fprintf(sb, "    <mxCell id=\"%s\" value=\"%s\" style=\"curved=1;endArrow=blockThin;endFill=1;fontSize=11;\" edge=\"1\" source=\"%s\" target=\"%s\" parent=\"1\">\n",
		edgeID, xmlEscape(label), SanitizeID(e.Source), SanitizeID(e.Target))
	sb.WriteString("      <mxGeometry relative=\"1\" as=\"geometry\"/>\n")
	sb.WriteString("    </mxCell>\n")
}

// drawIOStyleByType maps each NodeType to its mxCell style.
var drawIOStyleByType = map[model.NodeType]string{
	model.NodeDatabase:    "shape=cylinder3;whiteSpace=wrap;html=1;boundedLbl=1;backgroundOutline=1;size=15;fillColor=#d5e8d4;strokeColor=#82b366;",
	model.NodeQueue:       "shape=parallelogram;whiteSpace=wrap;html=1;fillColor=#fff2cc;strokeColor=#d6b656;",
	model.NodeCache:       "rounded=1;whiteSpace=wrap;html=1;dashed=1;fillColor=#f8cecc;strokeColor=#b85450;",
	model.NodeExternalAPI: "shape=cloud;whiteSpace=wrap;html=1;fillColor=#e1d5e7;strokeColor=#9673a6;",
	model.NodeEndpoint:    "shape=hexagon;whiteSpace=wrap;html=1;fillColor=#f5f5f5;strokeColor=#666666;",
}

const drawIODefaultStyle = "rounded=1;whiteSpace=wrap;html=1;fillColor=#dae8fc;strokeColor=#6c8ebf;"

func drawIOStyle(t model.NodeType) string {
	if s, ok := drawIOStyleByType[t]; ok {
		return s
	}
	return drawIODefaultStyle
}

// drawIOGroupLabels maps each NodeType to its cluster label.
var drawIOGroupLabels = map[model.NodeType]string{
	model.NodeService:     "Services",
	model.NodeDatabase:    "Data Stores",
	model.NodeQueue:       "Queues",
	model.NodeCache:       "Caches",
	model.NodeExternalAPI: "External",
	model.NodeEndpoint:    "Endpoints",
	model.NodePackage:     "Packages",
	model.NodeModule:      "Modules",
}

func drawIOGroupLabel(t model.NodeType) string {
	if l, ok := drawIOGroupLabels[t]; ok {
		return l
	}
	return "Components"
}

// drawIOGroupColors maps each NodeType to its cluster outline color.
var drawIOGroupColors = map[model.NodeType]string{
	model.NodeDatabase:    "#82b366",
	model.NodeQueue:       "#d6b656",
	model.NodeCache:       "#b85450",
	model.NodeExternalAPI: "#9673a6",
	model.NodeEndpoint:    "#666666",
}

const drawIODefaultGroupColor = "#6c8ebf"

func drawIOGroupColor(t model.NodeType) string {
	if c, ok := drawIOGroupColors[t]; ok {
		return c
	}
	return drawIODefaultGroupColor
}

func xmlEscape(s string) string {
	r := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		"\"", "&quot;",
		"'", "&apos;",
	)
	return r.Replace(s)
}
