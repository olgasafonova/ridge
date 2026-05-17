package render

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"html"
	"sort"
	"strings"

	"github.com/olgasafonova/ridge/internal/model"
)

// d3JS is the D3.js runtime, embedded for offline rendering.
// See assets/SOURCES.md for version, source URL, and refresh procedure.
//
//go:embed assets/d3.min.js
var d3JS string

// fgNode is the JSON shape sent to the D3 simulation.
type fgNode struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	Group     int    `json:"group"`
	Component int    `json:"component"`
}

// fgLink is the JSON shape sent to the D3 simulation.
type fgLink struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Type   string `json:"type"`
}

// ForceGraph renders an ArchGraph as a single self-contained HTML document with
// a D3-driven force-directed layout. Suited to hub-spoke graphs (knowledge
// vaults, doc trees, dense dependency networks) where Mermaid's hierarchical
// layout produces a long horizontal stripe.
//
// Output is roughly 290 KB (D3 ~273 KB plus the diagram JSON and a small
// wrapper). The page supports zoom (wheel), pan (drag background), and
// node drag (drag node).
//
// Color is keyed off node type. Node radius scales with degree (in + out)
// so hubs visually pop. Labels appear next to each node.
func ForceGraph(graph *model.ArchGraph, opts Options) string {
	vg := PrepareGraph(graph, opts)

	groupOf := nodeTypeGroup()
	components := connectedComponents(vg)

	jsonNodes := make([]fgNode, 0, len(vg.Nodes))
	for _, n := range vg.Nodes {
		jsonNodes = append(jsonNodes, fgNode{
			ID:        n.ID,
			Name:      n.Name,
			Type:      string(n.Type),
			Group:     groupOf[n.Type],
			Component: components[n.ID],
		})
	}

	jsonLinks := make([]fgLink, 0, len(vg.Edges))
	for _, e := range vg.Edges {
		jsonLinks = append(jsonLinks, fgLink{
			Source: e.Source,
			Target: e.Target,
			Type:   string(e.Type),
		})
	}

	nodesJSON, _ := json.Marshal(jsonNodes)
	linksJSON, _ := json.Marshal(jsonLinks)

	title := opts.Title
	if title == "" {
		title = "Architecture force-directed graph"
	}

	var sb strings.Builder
	sb.Grow(len(d3JS) + len(nodesJSON) + len(linksJSON) + 4096)

	sb.WriteString("<!doctype html>\n")
	sb.WriteString("<html lang=\"en\">\n<head>\n")
	sb.WriteString("<meta charset=\"utf-8\">\n")
	fmt.Fprintf(&sb, "<title>%s</title>\n", html.EscapeString(title))
	sb.WriteString("<style>")
	sb.WriteString("html,body{margin:0;padding:0;height:100%;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',sans-serif;background:#fafafa;color:#111}")
	sb.WriteString("#header{padding:12px 24px;background:#fff;border-bottom:1px solid #e5e7eb;display:flex;align-items:baseline;gap:16px}")
	sb.WriteString("#header h1{font-size:16px;margin:0;font-weight:600}")
	sb.WriteString("#header .meta{font-size:12px;color:#6b7280}")
	sb.WriteString("#chart{width:100%;height:calc(100vh - 50px);background:#fff}")
	sb.WriteString(".node{cursor:grab;stroke:#1f2937;stroke-width:1px}")
	sb.WriteString(".node:active{cursor:grabbing}")
	sb.WriteString(".link{stroke:#9ca3af;stroke-opacity:.5}")
	sb.WriteString(".label{font-size:10px;fill:#374151;pointer-events:none;user-select:none}")
	sb.WriteString(".label-hub{font-size:12px;font-weight:600;fill:#111}")
	sb.WriteString(".label-leaf{visibility:hidden}")
	sb.WriteString(".label-leaf.show{visibility:visible;font-size:11px;fill:#111;font-weight:500}")
	sb.WriteString("</style>\n</head>\n<body>\n")
	sb.WriteString("<div id=\"header\">")
	fmt.Fprintf(&sb, "<h1>%s</h1>", html.EscapeString(title))
	fmt.Fprintf(&sb, "<span class=\"meta\">%d nodes, %d edges, %d connected components (color = component). Drag nodes to rearrange. Wheel to zoom.</span>",
		len(jsonNodes), len(jsonLinks), componentCount(jsonNodes))
	sb.WriteString("</div>\n")
	sb.WriteString("<svg id=\"chart\"></svg>\n")
	sb.WriteString("<script>\n")
	sb.WriteString(d3JS)
	sb.WriteString("\n</script>\n")
	sb.WriteString("<script>\n")
	fmt.Fprintf(&sb, "const NODES = %s;\n", nodesJSON)
	fmt.Fprintf(&sb, "const LINKS = %s;\n", linksJSON)
	sb.WriteString(forceGraphScript)
	sb.WriteString("\n</script>\n")
	sb.WriteString("</body>\n</html>\n")
	return sb.String()
}

// componentInfo carries a connected component's member IDs and its minimum
// member ID, used to break size-ties when assigning indices.
type componentInfo struct {
	members []string
	minID   string
}

// connectedComponents labels each node with a component index using
// undirected union-find on the visible edges. Component 0 is the largest
// component; smaller components get sequentially higher indices. Indices
// are stable across runs for a given graph (sorted by size, then by the
// lowest member ID for tie-breaking).
func connectedComponents(vg *VisibleGraph) map[string]int {
	parent := initUnionFindParents(vg.Nodes)
	unionVisibleEdges(parent, vg.Edges)

	groups := groupByRoot(parent, vg.Nodes)
	infos := makeComponentInfos(groups)
	sortComponentInfos(infos)
	return assignComponentIndices(infos, len(vg.Nodes))
}

// initUnionFindParents seeds the union-find parent map with self-parents.
func initUnionFindParents(nodes []*model.Node) map[string]string {
	parent := make(map[string]string, len(nodes))
	for _, n := range nodes {
		parent[n.ID] = n.ID
	}
	return parent
}

// unionVisibleEdges unions endpoints of each edge whose endpoints both exist
// in the parent map. Edges referencing unknown nodes are skipped.
func unionVisibleEdges(parent map[string]string, edges []*model.Edge) {
	find := func(x string) string {
		for parent[x] != x {
			parent[x] = parent[parent[x]]
			x = parent[x]
		}
		return x
	}
	for _, e := range edges {
		if !edgeEndpointsKnown(parent, e) {
			continue
		}
		ra, rb := find(e.Source), find(e.Target)
		if ra != rb {
			parent[ra] = rb
		}
	}
}

// edgeEndpointsKnown reports whether both endpoints of the edge exist in the
// parent map.
func edgeEndpointsKnown(parent map[string]string, e *model.Edge) bool {
	if _, ok := parent[e.Source]; !ok {
		return false
	}
	_, ok := parent[e.Target]
	return ok
}

// groupByRoot collects each node ID under its union-find root.
func groupByRoot(parent map[string]string, nodes []*model.Node) map[string][]string {
	find := func(x string) string {
		for parent[x] != x {
			parent[x] = parent[parent[x]]
			x = parent[x]
		}
		return x
	}
	groups := make(map[string][]string)
	for _, n := range nodes {
		root := find(n.ID)
		groups[root] = append(groups[root], n.ID)
	}
	return groups
}

// makeComponentInfos builds componentInfo records (members + minID) for each
// group.
func makeComponentInfos(groups map[string][]string) []componentInfo {
	infos := make([]componentInfo, 0, len(groups))
	for _, members := range groups {
		infos = append(infos, componentInfo{members: members, minID: minMemberID(members)})
	}
	return infos
}

// minMemberID returns the lexicographically smallest ID in the slice. The
// slice must be non-empty.
func minMemberID(members []string) string {
	minID := members[0]
	for _, m := range members[1:] {
		if m < minID {
			minID = m
		}
	}
	return minID
}

// sortComponentInfos orders by size descending, then by minID ascending.
func sortComponentInfos(infos []componentInfo) {
	sort.Slice(infos, func(i, j int) bool {
		if len(infos[i].members) != len(infos[j].members) {
			return len(infos[i].members) > len(infos[j].members)
		}
		return infos[i].minID < infos[j].minID
	})
}

// assignComponentIndices maps each node ID to its component's index in infos.
func assignComponentIndices(infos []componentInfo, nodeCount int) map[string]int {
	out := make(map[string]int, nodeCount)
	for i, info := range infos {
		for _, id := range info.members {
			out[id] = i
		}
	}
	return out
}

// componentCount returns one more than the max component index seen.
func componentCount(nodes []fgNode) int {
	maxC := -1
	for _, n := range nodes {
		if n.Component > maxC {
			maxC = n.Component
		}
	}
	return maxC + 1
}

// nodeTypeGroup assigns each node type a group index used to pick a color
// in the D3 categorical palette.
func nodeTypeGroup() map[model.NodeType]int {
	return map[model.NodeType]int{
		model.NodeService:     0,
		model.NodeModule:      1,
		model.NodePackage:     2,
		model.NodeDatabase:    3,
		model.NodeQueue:       4,
		model.NodeCache:       5,
		model.NodeExternalAPI: 6,
		model.NodeEndpoint:    7,
		model.NodeNote:        8,
	}
}

// forceGraphScript is the client-side D3 simulation. Kept as a constant
// rather than a separate file so it ships in one go:embed-free unit.
const forceGraphScript = `
const svg = d3.select("#chart");
const width = svg.node().clientWidth;
const height = svg.node().clientHeight;

const palette = d3.schemeTableau10;

const degree = new Map();
LINKS.forEach(l => {
  degree.set(l.source, (degree.get(l.source) || 0) + 1);
  degree.set(l.target, (degree.get(l.target) || 0) + 1);
});
NODES.forEach(n => { n.deg = degree.get(n.id) || 0; });

const maxDeg = d3.max(NODES, n => n.deg) || 1;
const radius = d3.scaleSqrt().domain([0, maxDeg]).range([4, 22]);
const hubThreshold = Math.max(5, maxDeg * 0.3);

const root = svg.append("g");

svg.call(d3.zoom().scaleExtent([0.1, 8]).on("zoom", (e) => {
  root.attr("transform", e.transform);
}));

const link = root.append("g").attr("class", "links")
  .selectAll("line").data(LINKS).join("line")
  .attr("class", "link").attr("stroke-width", 1);

const node = root.append("g").attr("class", "nodes")
  .selectAll("circle").data(NODES).join("circle")
  .attr("class", "node")
  .attr("r", d => radius(d.deg))
  .attr("fill", d => palette[d.component % palette.length])
  .call(drag());

node.append("title").text(d =>
  d.name + " (degree=" + d.deg + ", component=" + d.component +
  (d.component === 0 ? " — main" : " — island") + ")");

const label = root.append("g").attr("class", "labels")
  .selectAll("text").data(NODES).join("text")
  .attr("class", d => d.deg >= hubThreshold ? "label label-hub" : "label label-leaf")
  .attr("dx", d => radius(d.deg) + 3)
  .attr("dy", "0.35em")
  .text(d => d.name);

// Reveal a leaf node's label on hover; hide it on leave. Hub labels are
// always visible.
node.on("mouseover", function(e, d) {
  label.filter(l => l.id === d.id).classed("show", true);
}).on("mouseout", function(e, d) {
  label.filter(l => l.id === d.id).classed("show", false);
});

const sim = d3.forceSimulation(NODES)
  .force("link", d3.forceLink(LINKS).id(d => d.id).distance(50).strength(0.5))
  .force("charge", d3.forceManyBody().strength(-110))
  .force("center", d3.forceCenter(width / 2, height / 2))
  .force("x", d3.forceX(width / 2).strength(0.06))
  .force("y", d3.forceY(height / 2).strength(0.06))
  .force("collide", d3.forceCollide().radius(d => radius(d.deg) + 4))
  .on("tick", () => {
    link
      .attr("x1", d => d.source.x).attr("y1", d => d.source.y)
      .attr("x2", d => d.target.x).attr("y2", d => d.target.y);
    node.attr("cx", d => d.x).attr("cy", d => d.y);
    label.attr("x", d => d.x).attr("y", d => d.y);
  });

function drag() {
  return d3.drag()
    .on("start", (e, d) => { if (!e.active) sim.alphaTarget(0.3).restart(); d.fx = d.x; d.fy = d.y; })
    .on("drag",  (e, d) => { d.fx = e.x; d.fy = e.y; })
    .on("end",   (e, d) => { if (!e.active) sim.alphaTarget(0); d.fx = null; d.fy = null; });
}
`
