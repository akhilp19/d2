package d2layouts

import (
	"context"
	"math"
	"strings"

	"oss.terrastruct.com/d2/d2graph"
	"oss.terrastruct.com/d2/d2layouts/d2grid"
	"oss.terrastruct.com/d2/d2layouts/d2near"
	"oss.terrastruct.com/d2/d2layouts/d2sequence"
	"oss.terrastruct.com/d2/lib/geo"
)

type DiagramType string

// a grid diagram at a constant near is
const (
	DefaultGraphType  DiagramType = ""
	ConstantNearGraph DiagramType = "constant-near"
	GridDiagram       DiagramType = "grid-diagram"
	SequenceDiagram   DiagramType = "sequence-diagram"
)

type GraphInfo struct {
	IsConstantNear bool
	DiagramType    DiagramType
}

func (gi GraphInfo) isDefault() bool {
	return !gi.IsConstantNear && gi.DiagramType == DefaultGraphType
}

func LayoutNested(ctx context.Context, g *d2graph.Graph, graphInfo GraphInfo, coreLayout d2graph.LayoutGraph) geo.Spacing {

	// Before we can layout these nodes, we need to handle all nested diagrams first.
	extracted := make(map[*d2graph.Object]*d2graph.Graph)
	extractedInfo := make(map[*d2graph.Object]GraphInfo)

	var constantNears []*d2graph.Graph

	// Iterate top-down from Root so all nested diagrams can process their own contents
	queue := make([]*d2graph.Object, 0, len(g.Root.ChildrenArray))
	if graphInfo.IsConstantNear {
		near := g.Root.ChildrenArray[0]
		if len(near.Children) > 0 {
			queue = append(queue, near.ChildrenArray...)
		}
	} else {
		queue = append(queue, g.Root.ChildrenArray...)
	}

	for _, child := range queue {
		if gi := NestedGraphInfo(child); !gi.isDefault() {
			extractedInfo[child] = gi

			var nestedGraph *d2graph.Graph
			if gi.IsConstantNear {
				nestedGraph = ExtractSelf(child)
			} else {
				// There is a nested diagram here, so extract its contents and process in the same way
				nestedGraph = ExtractDescendants(child)
			}

			// Layout of nestedGraph is completed
			spacing := LayoutNested(ctx, nestedGraph, gi, coreLayout)
			if !gi.IsConstantNear {
				// Fit child to size of nested layout
				FitToGraph(child, nestedGraph, spacing)
			}

			// We will restore the contents after running layout with child as the placeholder
			if gi.IsConstantNear {
				constantNears = append(constantNears, nestedGraph)
			} else {
				extracted[child] = nestedGraph
			}
		} else if len(child.Children) > 0 {
			queue = append(queue, child.ChildrenArray...)
		}
	}

	// We can now run layout with accurate sizes of nested layout containers
	// Layout according to the type of diagram
	LayoutDiagram := func(ctx context.Context, g *d2graph.Graph, graphInfo GraphInfo, coreLayout d2graph.LayoutGraph) geo.Spacing {
		spacing := geo.Spacing{}
		var err error
		// TODO

		switch graphInfo.DiagramType {
		case GridDiagram:
			layoutWithGrids := d2grid.Layout(ctx, g, coreLayout)
			if err = layoutWithGrids(ctx, g); err != nil {
				panic(err)
			}

		case SequenceDiagram:
			err = d2sequence.Layout2(ctx, g, coreLayout)
			if err != nil {
				panic(err)
			}
		default:
			err := coreLayout(ctx, g)
			if err != nil {
				panic(err)
			}
		}
		return spacing
	}
	spacing := LayoutDiagram(ctx, g, graphInfo, coreLayout)

	// With the layout set, inject all the extracted graphs
	for n, nestedGraph := range extracted {
		if !extractedInfo[n].IsConstantNear {
			InjectNested(n, nestedGraph)
			PositionNested(n, nestedGraph)
		}
	}

	// if there are
	if len(constantNears) > 0 {
		err := d2near.Layout(ctx, g, constantNears)
		if err != nil {
			panic(err)
		}
	}

	return spacing
}

// TODO multiple types at same (e.g. constant nears with grid at root level)
// e.g. constant nears with sequence diagram at root level
func NestedGraphInfo(obj *d2graph.Object) (gi GraphInfo) {
	if obj.Graph.RootLevel == 0 && obj.IsConstantNear() {
		gi.IsConstantNear = true
	}
	// if obj.Graph.RootLevel == -1 {
	// 	for _, obj := range obj.Graph.Root.ChildrenArray {
	// 		if obj.IsConstantNear() {
	// 			return ConstantNearGraph
	// 		}
	// 	}
	// }
	if obj.IsSequenceDiagram() {
		gi.DiagramType = SequenceDiagram
	} else if obj.IsGridDiagram() {
		gi.DiagramType = GridDiagram
	}
	return gi
}

func ExtractSelf(container *d2graph.Object) *d2graph.Graph {
	nestedGraph := d2graph.NewGraph()
	nestedGraph.RootLevel = int(container.Level()) - 1

	// separate out nested edges
	g := container.Graph
	remainingEdges := make([]*d2graph.Edge, 0, len(g.Edges))
	for _, edge := range g.Edges {
		if edge.Src.IsDescendantOf(container) && edge.Dst.IsDescendantOf(container) {
			nestedGraph.Edges = append(nestedGraph.Edges, edge)
		} else {
			remainingEdges = append(remainingEdges, edge)
		}
	}
	g.Edges = remainingEdges

	// separate out nested objects
	remainingObjects := make([]*d2graph.Object, 0, len(g.Objects))
	for _, obj := range g.Objects {
		if obj.IsDescendantOf(container) {
			nestedGraph.Objects = append(nestedGraph.Objects, obj)
		} else {
			remainingObjects = append(remainingObjects, obj)
		}
	}
	g.Objects = remainingObjects

	// update object and new root references
	for _, o := range nestedGraph.Objects {
		o.Graph = nestedGraph
	}

	// remove container parent's references
	if container.Parent != nil {
		container.Parent.RemoveChild(container)
	}

	// set root references
	nestedGraph.Root.ChildrenArray = []*d2graph.Object{container}
	container.Parent = nestedGraph.Root
	nestedGraph.Root.Children[strings.ToLower(container.ID)] = container

	return nestedGraph
}

func ExtractDescendants(container *d2graph.Object) *d2graph.Graph {
	nestedGraph := d2graph.NewGraph()
	nestedGraph.RootLevel = int(container.Level())
	nestedGraph.Root.Box = &geo.Box{}

	// separate out nested edges
	g := container.Graph
	remainingEdges := make([]*d2graph.Edge, 0, len(g.Edges))
	for _, edge := range g.Edges {
		if edge.Src.Parent.IsDescendantOf(container) && edge.Dst.Parent.IsDescendantOf(container) {
			nestedGraph.Edges = append(nestedGraph.Edges, edge)
		} else {
			remainingEdges = append(remainingEdges, edge)
		}
	}
	g.Edges = remainingEdges

	// separate out nested objects
	remainingObjects := make([]*d2graph.Object, 0, len(g.Objects))
	for _, obj := range g.Objects {
		if obj.Parent.IsDescendantOf(container) {
			nestedGraph.Objects = append(nestedGraph.Objects, obj)
		} else {
			remainingObjects = append(remainingObjects, obj)
		}
	}
	g.Objects = remainingObjects

	// update object and new root references
	for _, o := range nestedGraph.Objects {
		o.Graph = nestedGraph
	}
	// set root references
	nestedGraph.Root.ChildrenArray = append(nestedGraph.Root.ChildrenArray, container.ChildrenArray...)
	for _, child := range container.ChildrenArray {
		child.Parent = nestedGraph.Root
		nestedGraph.Root.Children[strings.ToLower(child.ID)] = child
	}

	// remove container's references
	for k := range container.Children {
		delete(container.Children, k)
	}
	container.ChildrenArray = nil

	return nestedGraph
}

func InjectNested(container *d2graph.Object, nestedGraph *d2graph.Graph) {
	g := container.Graph
	for _, obj := range nestedGraph.Root.ChildrenArray {
		obj.Parent = container
		container.Children[strings.ToLower(obj.ID)] = obj
		container.ChildrenArray = append(container.ChildrenArray, obj)
	}
	for _, obj := range nestedGraph.Objects {
		obj.Graph = g
	}
	g.Objects = append(g.Objects, nestedGraph.Objects...)
	g.Edges = append(g.Edges, nestedGraph.Edges...)

}

func PositionNested(container *d2graph.Object, nestedGraph *d2graph.Graph) {
	tl, _ := boundingBox(nestedGraph)
	// Note: assumes nestedGraph's layout has contents positioned relative to 0,0
	dx := container.TopLeft.X - tl.X
	dy := container.TopLeft.Y - tl.Y
	for _, o := range nestedGraph.Objects {
		o.TopLeft.X += dx
		o.TopLeft.Y += dy
	}
	for _, e := range nestedGraph.Edges {
		e.Move(dx, dy)
	}
}

func boundingBox(g *d2graph.Graph) (tl, br *geo.Point) {
	if len(g.Objects) == 0 {
		return geo.NewPoint(0, 0), geo.NewPoint(0, 0)
	}
	tl = geo.NewPoint(math.Inf(1), math.Inf(1))
	br = geo.NewPoint(math.Inf(-1), math.Inf(-1))

	for _, obj := range g.Objects {
		tl.X = math.Min(tl.X, obj.TopLeft.X)
		tl.Y = math.Min(tl.Y, obj.TopLeft.Y)
		br.X = math.Max(br.X, obj.TopLeft.X+obj.Width)
		br.Y = math.Max(br.Y, obj.TopLeft.Y+obj.Height)
	}

	return tl, br
}

func FitToGraph(container *d2graph.Object, nestedGraph *d2graph.Graph, padding geo.Spacing) {
	tl, br := boundingBox(nestedGraph)
	container.Width = padding.Left + br.X - tl.X + padding.Right
	container.Height = padding.Top + br.Y - tl.Y + padding.Bottom
}

// func LayoutDiagram(ctx context.Context, g *d2graph.Graph, graphInfo GraphInfo, coreLayout d2graph.LayoutGraph) geo.Spacing {
// 	spacing := geo.Spacing{}
// 	var err error
// 	// TODO

// 	// Need subgraphs?

// 	// if graphInfo.IsConstantNear
// 	// case ConstantNearGraph:
// 	// 	// constantNearGraphs := d2near.WithoutConstantNears(ctx, g)
// 	// 	constantNearGraphs := d2near.WithoutConstantNears(ctx, g)

// 	// 	err = d2near.Layout(ctx, g, constantNearGraphs)
// 	// 	if err != nil {
// 	// 		panic(err)
// 	// 	}

// 	switch graphInfo.DiagramType {
// 	case GridDiagram:
// 		layoutWithGrids := d2grid.Layout(ctx, g, coreLayout)
// 		if err = layoutWithGrids(ctx, g); err != nil {
// 			panic(err)
// 		}

// 	case SequenceDiagram:
// 		err = d2sequence.Layout(ctx, g, coreLayout)
// 		if err != nil {
// 			panic(err)
// 		}
// 	default:
// 		err := coreLayout(ctx, g)
// 		if err != nil {
// 			panic(err)
// 		}
// 	}

// 	return spacing
// }
