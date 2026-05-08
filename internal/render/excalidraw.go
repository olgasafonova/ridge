package render

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/olgasafonova/ridge/internal/model"
)

const (
	excaliCellW    = 200
	excaliCellH    = 60
	excaliGapX     = 100
	excaliGapY     = 300
	excaliMarginX  = 60
	excaliMarginY  = 60
	excaliGroupPad = 16
)

// excaliPortAssign carries the start/end attachment fractions for one edge.
type excaliPortAssign struct {
	startFrac, endFrac float64
	outRank, outTotal  int
}

// excaliBounds tracks the rectangular bounds of a node-type group.
type excaliBounds struct {
	minX, minY, maxX, maxY int
}

// excaliSeed produces deterministic, monotonically-increasing seeds so the
// same graph renders identically across runs.
type excaliSeed struct{ v int }

func (s *excaliSeed) next() int {
	s.v += 7
	return s.v
}

// Excalidraw renders an ArchGraph as Excalidraw JSON.
// Nodes are arranged in topological layers: root packages at the top,
// leaf dependencies at the bottom. Arrows use orthogonal routing.
func Excalidraw(graph *model.ArchGraph, opts Options) string {
	vg := PrepareGraph(graph, opts)
	vg.TransitiveReduce()

	depths, maxDepth := ComputeNodeDepths(vg)
	layers := LayerByDepth(vg.Nodes, depths, maxDepth)
	BarycenterOrder(layers, vg.Edges)

	seed := &excaliSeed{v: 1000000}
	maxWidth := excaliLayoutWidth(layers)

	nodeElements, positions, groupBounds := excaliLayoutNodes(layers, maxDepth, maxWidth, seed)
	groupElements := excaliBuildGroupFrames(groupBounds, seed)

	portMap := excaliDistributePorts(vg, positions)
	arrowElements := excaliBuildArrows(vg, portMap, positions, seed)

	elements := make([]map[string]any, 0, len(groupElements)+len(nodeElements)+len(arrowElements))
	elements = append(elements, groupElements...)
	elements = append(elements, nodeElements...)
	elements = append(elements, arrowElements...)

	return excaliMarshal(elements)
}

// excaliLayoutWidth returns the canvas width needed for the widest layer.
func excaliLayoutWidth(layers [][]*model.Node) int {
	maxCount := 0
	for _, nodes := range layers {
		if len(nodes) > maxCount {
			maxCount = len(nodes)
		}
	}
	if maxCount == 0 {
		maxCount = 1
	}
	return maxCount*excaliCellW + (maxCount-1)*excaliGapX
}

// excaliLayoutNodes assigns a position to each node, emits its rect+text
// elements, and tracks the bounds of each NodeType group.
func excaliLayoutNodes(layers [][]*model.Node, maxDepth, maxWidth int, seed *excaliSeed) ([]map[string]any, map[string][2]int, map[model.NodeType]*excaliBounds) {
	elements := make([]map[string]any, 0)
	positions := make(map[string][2]int)
	groupBounds := make(map[model.NodeType]*excaliBounds)

	for d := maxDepth; d >= 0; d-- {
		elements, positions = excaliEmitLayer(elements, positions, groupBounds, layers[d], maxDepth-d, maxWidth, seed)
	}
	return elements, positions, groupBounds
}

// excaliEmitLayer positions one row of nodes, emits their rect+text, and
// updates per-type group bounds.
func excaliEmitLayer(elements []map[string]any, positions map[string][2]int, groupBounds map[model.NodeType]*excaliBounds, nodes []*model.Node, row, maxWidth int, seed *excaliSeed) ([]map[string]any, map[string][2]int) {
	layerWidth := len(nodes)*excaliCellW + (len(nodes)-1)*excaliGapX
	offsetX := (maxWidth - layerWidth) / 2
	y := excaliMarginY + row*(excaliCellH+excaliGapY)

	for j, n := range nodes {
		x := excaliMarginX + offsetX + j*(excaliCellW+excaliGapX)
		rectID := SanitizeID(n.ID)
		positions[n.ID] = [2]int{x, y}

		elements = append(elements, excaliRect(excaliRectSpec{
			id:      rectID,
			box:     excaliBox{x: x, y: y, w: excaliCellW, h: excaliCellH},
			bgColor: excalidrawBgColor(n.Type),
			seed:    seed.next(),
		}))
		elements = append(elements, excaliText(excaliTextSpec{
			id:        rectID + "_text",
			box:       excaliBox{x: x + 10, y: y + 20, w: excaliCellW - 20, h: 25},
			text:      n.Name,
			fontSize:  16,
			container: &rectID,
			seed:      seed.next(),
		}))

		expandGroupBounds(groupBounds, n.Type, x, y)
	}
	return elements, positions
}

// expandGroupBounds widens (or initializes) the bounds for a NodeType group
// to enclose a cell at (x, y).
func expandGroupBounds(groupBounds map[model.NodeType]*excaliBounds, t model.NodeType, x, y int) {
	b := groupBounds[t]
	if b == nil {
		groupBounds[t] = &excaliBounds{minX: x, minY: y, maxX: x + excaliCellW, maxY: y + excaliCellH}
		return
	}
	if x < b.minX {
		b.minX = x
	}
	if y < b.minY {
		b.minY = y
	}
	if x+excaliCellW > b.maxX {
		b.maxX = x + excaliCellW
	}
	if y+excaliCellH > b.maxY {
		b.maxY = y + excaliCellH
	}
}

// excaliBuildGroupFrames emits a translucent backdrop rect + label for each
// NodeType cluster. Returned in iteration order (Excalidraw renders in the
// order it gets, so callers must prepend to keep frames behind nodes).
func excaliBuildGroupFrames(groupBounds map[model.NodeType]*excaliBounds, seed *excaliSeed) []map[string]any {
	if len(groupBounds) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, 2*len(groupBounds))
	for nt, b := range groupBounds {
		out = append(out, excaliGroupFrameElements(nt, b, seed)...)
	}
	return out
}

// excaliGroupFrameElements returns the [frame-rect, label-text] pair for one cluster.
func excaliGroupFrameElements(nt model.NodeType, b *excaliBounds, seed *excaliSeed) []map[string]any {
	gx := b.minX - excaliGroupPad
	gy := b.minY - excaliGroupPad - 24
	gw := b.maxX - b.minX + 2*excaliGroupPad
	gh := b.maxY - b.minY + 2*excaliGroupPad + 24

	frameID := fmt.Sprintf("group_%s", SanitizeID(string(nt)))
	frame := excaliRect(excaliRectSpec{
		id:      frameID,
		box:     excaliBox{x: gx, y: gy, w: gw, h: gh},
		bgColor: excalidrawBgColor(nt),
		seed:    seed.next(),
	})
	frame["opacity"] = 20
	frame["strokeColor"] = excalidrawBgColor(nt)

	label := excaliText(excaliTextSpec{
		id:       frameID + "_label",
		box:      excaliBox{x: gx + 8, y: gy + 4, w: gw - 16, h: 20},
		text:     drawIOGroupLabel(nt),
		fontSize: 12,
		seed:     seed.next(),
	})
	return []map[string]any{frame, label}
}

// excaliDistributePorts assigns start/end attachment fractions across each
// node's width so multiple edges sharing a source or target are visually
// separated rather than overlapping at the cell midpoint.
func excaliDistributePorts(vg *VisibleGraph, positions map[string][2]int) map[int]excaliPortAssign {
	portMap := make(map[int]excaliPortAssign, len(vg.Edges))

	edgesBySource := make(map[string][]int)
	edgesByTarget := make(map[string][]int)
	for i, e := range vg.Edges {
		edgesBySource[e.Source] = append(edgesBySource[e.Source], i)
		edgesByTarget[e.Target] = append(edgesByTarget[e.Target], i)
	}

	excaliAssignFractions(portMap, edgesBySource, vg, positions, true)
	excaliAssignFractions(portMap, edgesByTarget, vg, positions, false)
	return portMap
}

// excaliAssignFractions sorts edges sharing a source (or target) by the
// position of the opposite endpoint, then spreads start (or end) fractions
// across [0.15, 0.85].
func excaliAssignFractions(portMap map[int]excaliPortAssign, groups map[string][]int, vg *VisibleGraph, positions map[string][2]int, isSource bool) {
	for _, indices := range groups {
		assignFractionsForGroup(portMap, indices, vg, positions, isSource)
	}
}

func assignFractionsForGroup(portMap map[int]excaliPortAssign, indices []int, vg *VisibleGraph, positions map[string][2]int, isSource bool) {
	sort.Slice(indices, func(a, b int) bool {
		return positions[oppositeEnd(vg.Edges[indices[a]], isSource)][0] <
			positions[oppositeEnd(vg.Edges[indices[b]], isSource)][0]
	})
	n := len(indices)
	for rank, idx := range indices {
		portMap[idx] = updatedPortAssign(portMap[idx], rank, n, isSource)
	}
}

// oppositeEnd returns the endpoint we sort against: when ordering an
// outgoing-edge group we sort by target X, when ordering incoming we sort by source X.
func oppositeEnd(e *model.Edge, isSource bool) string {
	if isSource {
		return e.Target
	}
	return e.Source
}

func updatedPortAssign(pa excaliPortAssign, rank, n int, isSource bool) excaliPortAssign {
	frac := portFraction(rank, n)
	if isSource {
		pa.startFrac = frac
		pa.outRank = rank
		pa.outTotal = n
		return pa
	}
	pa.endFrac = frac
	return pa
}

func portFraction(rank, n int) float64 {
	if n <= 1 {
		return 0.5
	}
	return 0.15 + 0.7*float64(rank)/float64(n-1)
}

// excaliBuildArrows produces the arrow elements with orthogonal routing.
func excaliBuildArrows(vg *VisibleGraph, portMap map[int]excaliPortAssign, positions map[string][2]int, seed *excaliSeed) []map[string]any {
	out := make([]map[string]any, 0, len(vg.Edges))
	for i, e := range vg.Edges {
		out = append(out, excaliArrowFor(i, e, portMap[i], positions, seed))
	}
	return out
}

func excaliArrowFor(index int, e *model.Edge, pa excaliPortAssign, positions map[string][2]int, seed *excaliSeed) map[string]any {
	srcPos := positions[e.Source]
	tgtPos := positions[e.Target]

	startX := float64(srcPos[0]) + pa.startFrac*float64(excaliCellW)
	startY := float64(srcPos[1] + excaliCellH)
	endX := float64(tgtPos[0]) + pa.endFrac*float64(excaliCellW)
	endY := float64(tgtPos[1])

	return excaliArrow(
		fmt.Sprintf("arrow_%d", index),
		startX, startY,
		excaliRoutePoints(endX-startX, endY-startY, pa),
		seed.next(),
	)
}

// excaliRoutePoints returns the orthogonal-routing waypoints for an arrow.
// Short horizontal deltas use a single straight segment; longer ones detour
// through a fan-out channel keyed off the source's outgoing rank.
func excaliRoutePoints(dx, dy float64, pa excaliPortAssign) [][2]float64 {
	if absFloat(dx) < 10 {
		return [][2]float64{{0, 0}, {0, dy}}
	}
	channelDY := 30.0
	if pa.outTotal > 1 {
		channelDY = 30 + float64(excaliGapY-60)*float64(pa.outRank)/float64(pa.outTotal-1)
	}
	return [][2]float64{
		{0, 0},
		{0, channelDY},
		{dx, channelDY},
		{dx, dy},
	}
}

func absFloat(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

// excaliMarshal wraps a complete element list in the Excalidraw envelope.
func excaliMarshal(elements []map[string]any) string {
	file := map[string]any{
		"type":     "excalidraw",
		"version":  2,
		"source":   "ridge",
		"elements": elements,
		"appState": map[string]any{"viewBackgroundColor": "#ffffff"},
		"files":    map[string]any{},
	}
	data, _ := json.MarshalIndent(file, "", "  ")
	return string(data)
}

// excaliBox is the common geometry block for Excalidraw rect/text elements.
type excaliBox struct {
	x, y, w, h int
}

// excaliRectSpec bundles the inputs for excaliRect (was 7 positional args).
type excaliRectSpec struct {
	id      string
	box     excaliBox
	bgColor string
	seed    int
}

// excaliTextSpec bundles the inputs for excaliText (was 9 positional args).
type excaliTextSpec struct {
	id        string
	box       excaliBox
	text      string
	fontSize  int
	container *string
	seed      int
}

// excaliRect creates a complete Excalidraw rectangle element.
func excaliRect(spec excaliRectSpec) map[string]any {
	return map[string]any{
		"id":              spec.id,
		"type":            "rectangle",
		"x":               spec.box.x,
		"y":               spec.box.y,
		"width":           spec.box.w,
		"height":          spec.box.h,
		"angle":           0,
		"strokeColor":     "#1e1e1e",
		"backgroundColor": spec.bgColor,
		"fillStyle":       "solid",
		"strokeWidth":     2,
		"strokeStyle":     "solid",
		"roughness":       1,
		"opacity":         100,
		"groupIds":        []string{},
		"frameId":         nil,
		"roundness":       map[string]int{"type": 3},
		"seed":            spec.seed,
		"version":         1,
		"versionNonce":    spec.seed + 1,
		"isDeleted":       false,
		"boundElements":   nil,
		"updated":         1708000000000,
		"link":            nil,
		"locked":          false,
	}
}

// excaliText creates a complete Excalidraw text element.
func excaliText(spec excaliTextSpec) map[string]any {
	return map[string]any{
		"id":              spec.id,
		"type":            "text",
		"x":               spec.box.x,
		"y":               spec.box.y,
		"width":           spec.box.w,
		"height":          spec.box.h,
		"angle":           0,
		"strokeColor":     "#1e1e1e",
		"backgroundColor": "transparent",
		"fillStyle":       "solid",
		"strokeWidth":     2,
		"strokeStyle":     "solid",
		"roughness":       1,
		"opacity":         100,
		"groupIds":        []string{},
		"frameId":         nil,
		"roundness":       nil,
		"seed":            spec.seed,
		"version":         1,
		"versionNonce":    spec.seed + 1,
		"isDeleted":       false,
		"boundElements":   nil,
		"updated":         1708000000000,
		"link":            nil,
		"locked":          false,
		"text":            spec.text,
		"fontSize":        spec.fontSize,
		"fontFamily":      1,
		"textAlign":       "center",
		"verticalAlign":   "middle",
		"containerId":     spec.container,
		"originalText":    spec.text,
		"lineHeight":      1.25,
	}
}

// excaliArrow creates a complete Excalidraw arrow element with all
// required properties so Excalidraw doesn't re-normalize the path.
func excaliArrow(id string, x, y float64, points [][2]float64, seed int) map[string]any {
	minX, minY, maxX, maxY := boundsOfPoints(points)
	return map[string]any{
		"id":                 id,
		"type":               "arrow",
		"x":                  x,
		"y":                  y,
		"width":              maxX - minX,
		"height":             maxY - minY,
		"angle":              0,
		"strokeColor":        "#868e96",
		"backgroundColor":    "transparent",
		"fillStyle":          "solid",
		"strokeWidth":        1,
		"strokeStyle":        "solid",
		"roughness":          0,
		"opacity":            100,
		"groupIds":           []string{},
		"frameId":            nil,
		"roundness":          map[string]int{"type": 2},
		"seed":               seed,
		"version":            1,
		"versionNonce":       seed + 1,
		"isDeleted":          false,
		"boundElements":      nil,
		"updated":            1708000000000,
		"link":               nil,
		"locked":             false,
		"points":             points,
		"lastCommittedPoint": nil,
		"startBinding":       nil,
		"endBinding":         nil,
		"startArrowhead":     nil,
		"endArrowhead":       "arrow",
	}
}

// boundsOfPoints returns the axis-aligned bounding box of a polyline.
func boundsOfPoints(points [][2]float64) (minX, minY, maxX, maxY float64) {
	for _, p := range points {
		if p[0] < minX {
			minX = p[0]
		}
		if p[0] > maxX {
			maxX = p[0]
		}
		if p[1] < minY {
			minY = p[1]
		}
		if p[1] > maxY {
			maxY = p[1]
		}
	}
	return minX, minY, maxX, maxY
}

// excalidrawBgColors maps each NodeType to its rectangle background color.
var excalidrawBgColors = map[model.NodeType]string{
	model.NodeDatabase:    "#d5e8d4",
	model.NodeQueue:       "#fff2cc",
	model.NodeCache:       "#f8cecc",
	model.NodeExternalAPI: "#e1d5e7",
	model.NodeEndpoint:    "#f5f5f5",
}

const excalidrawDefaultBgColor = "#dae8fc"

func excalidrawBgColor(t model.NodeType) string {
	if c, ok := excalidrawBgColors[t]; ok {
		return c
	}
	return excalidrawDefaultBgColor
}
