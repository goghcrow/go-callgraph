package go_callgraph

import (
	"fmt"
	"go/ast"
	"go/build"
	"go/types"
	"net/url"
	"os/exec"
	"strconv"
	"strings"

	astmatcher "github.com/goghcrow/go-ast-matcher"
	"github.com/goghcrow/go-matcher"
	"github.com/goghcrow/go-matcher/combinator"
	"golang.org/x/tools/go/types/typeutil"
)

type StaticFlags struct {
	FunDeclPattern   *ast.FuncDecl
	CallExprPattern  *ast.CallExpr
	ShowOrphanedNode bool
}

type StaticOption func(*StaticFlags)

func WithCallerPattern(funPtn *ast.FuncDecl) StaticOption {
	return func(flags *StaticFlags) { flags.FunDeclPattern = funPtn }
}
func WithCallExprPattern(callPtn *ast.CallExpr) StaticOption {
	return func(flags *StaticFlags) { flags.CallExprPattern = callPtn }
}
func WithShowOrphanedNode() StaticOption {
	return func(flags *StaticFlags) { flags.ShowOrphanedNode = true }
}

// NewStatic ast-pattern-based call graph
func NewStatic(m astmatcher.ASTMatcher, opts ...StaticOption) *StaticCallGraph {
	flags := &StaticFlags{
		FunDeclPattern: &ast.FuncDecl{}, // all func decl,
		CallExprPattern: combinator.CalleeOf(m.Matcher, func(ctx *matcher.MatchCtx, obj types.Object) bool {
			return obj != nil
		}),
		ShowOrphanedNode: false,
	}
	for _, opt := range opts {
		opt(flags)
	}

	g := newGraph()

	m.Match(flags.FunDeclPattern, func(c astmatcher.Cursor, ctx astmatcher.Ctx) {
		funDecl := c.Node().(*ast.FuncDecl)
		caller := ctx.ObjectOf(funDecl.Name).(*types.Func)
		info := ctx.TypeInfo()

		if flags.ShowOrphanedNode {
			g.addNode(caller)
		}

		ctx.Match(flags.CallExprPattern, funDecl, func(c matcher.Cursor, ctx *matcher.MatchCtx) {
			node := c.Node()
			callExpr := node.(*ast.CallExpr)
			calleeObj := typeutil.Callee(info, callExpr)
			if calleeObj == nil {
				l := m.Loader
				if l.Flags.PrintErrors {
					errLog("empty callee object: " + l.ShowNodeWithPos(node))
				}
				return
			}

			switch callee := calleeObj.(type) {
			case *types.Func:
				g.addStaticEdge(callExpr, caller, callee)
			case *types.Var:
				g.addDynamicEdge(callExpr, caller, callee)
			case *types.Builtin:
				g.addBuiltinEdge(callExpr, caller, callee)
			default:
				panic("unreachable")
			}
		})
	})

	return g
}

type (
	ID       = string
	NodeType int
)

const (
	NodeFunc NodeType = iota + 1
	NodeRecv
	NodePkg
)

type Node struct {
	ID    ID       `json:"id"`
	PID   ID       `json:"parent"`
	Label string   `json:"label"`
	Desc  string   `json:"desc"`
	Attrs []string `json:"attrs"`

	Type   NodeType
	Object any // union of  *types.Func | *types.Var |*types.Package
}

type Edge struct {
	ID     ID       `json:"id"`
	Source ID       `json:"source"`
	Target ID       `json:"target"`
	Attrs  []string `json:"attrs"`
}

type StaticCallGraph struct {
	idCounter uint64
	idMap     map[string]ID

	Nodes map[ID]*Node
	Edges map[ID]*Edge
}

func newGraph() *StaticCallGraph {
	return &StaticCallGraph{
		idMap: make(map[string]ID),
		Nodes: make(map[ID]*Node),
		Edges: make(map[ID]*Edge),
	}
}

type Dot string

func (dot Dot) OpenOnline() {
	// https://dreampuf.github.io/GraphvizOnline
	// https://edotor.net/
	// https://www.devtoolsdaily.com/graphviz
	// http://magjac.com/graphviz-visual-editor/
	cmd := exec.Command("open", "https://dreampuf.github.io/GraphvizOnline/#"+url.PathEscape(string(dot)))
	_, _ = cmd.Output()
}

func (dot Dot) String() string {
	return string(dot)
}

func (g *StaticCallGraph) Dot() Dot {
	dot := strings.Builder{}
	dot.WriteString(`
digraph callgraph {
	outputorder=edgesfirst
	graph[rankdir=LR, center=true]
	node [color=grey, style=filled, fontname="Sans serif", fontsize=13]
	edge[arrowsize=0.6, arrowhead=vee, color=gray]

`)

	for id, node := range g.Nodes {
		switch node.Type {
		case NodeFunc:
			// isFun := strings.Index(node.Label, ".") == -1
			f := node.Object.(*types.Func)
			isFun := f.Type().(*types.Signature).Recv() == nil
			color, fontSize := "lightpink", 13
			if isFun {
				color = "lightblue"
			}
			// if f.Exported() { fontSize = 14 }
			label := node.Label
			// label += "\n" + strings.Join(node.Attrs, ",")
			dot.WriteString(fmt.Sprintf("\t%s [label=%q, fontsize=%d, color=%q, target=\"_top\"]\n",
				id, label, fontSize, color))
		case NodeRecv:
			color := "#3182bd"
			dot.WriteString(fmt.Sprintf("\t%s [label=%q, fontcolor=%q, target=\"_top\"]\n",
				id, node.Label, color))
		case NodePkg:
			color := "#3182bd"
			dot.WriteString(fmt.Sprintf("\t%s [label=%q, fontcolor=%q, target=\"_top\"]\n",
				id, node.Label, color))
		default:
			panic("unreached")
		}

	}
	for _, edge := range g.Edges {
		dot.WriteString(fmt.Sprintf("\t%s -> %s\n", edge.Source, edge.Target))
	}
	dot.WriteString("}")
	return Dot(dot.String())
}

func (g *StaticCallGraph) SubGraph(rootPred func(*Node) bool) *StaticCallGraph {
	findRoots := func() (roots []ID) {
		for id, node := range g.Nodes {
			if rootPred(node) {
				roots = append(roots, id)
			}
		}
		return
	}

	type (
		nodeID = ID
		edgeID = ID
		pair   = struct {
			edgeID
			nodeID
		}
	)
	edges := map[nodeID][]pair{}
	for eid, edge := range g.Edges {
		edges[edge.Source] = append(edges[edge.Source], pair{eid, edge.Target})
	}

	subGraph := newGraph()
	var dfs func(nodeID)
	dfs = func(source nodeID) {
		if subGraph.Nodes[source] != nil {
			return
		}
		subGraph.Nodes[source] = g.Nodes[source]
		for _, it := range edges[source] {
			subGraph.Edges[it.edgeID] = g.Edges[it.edgeID]
			dfs(it.nodeID)
		}
	}
	for _, root := range findRoots() {
		dfs(root)
	}
	return subGraph
}

func (g *StaticCallGraph) getID(fullName string, isNode bool) (id ID, isNew bool) {
	if id, ok := g.idMap[fullName]; ok {
		return id, false
	}

	g.idCounter++
	id = "e"
	if isNode {
		id = "n"
	}
	id += strconv.FormatUint(g.idCounter, 16)
	g.idMap[fullName] = id
	return id, true
}

func (g *StaticCallGraph) addNode(fun *types.Func) ID {
	funcName := g.funName(fun)
	fullName := fmt.Sprintf("func ~ %s", funcName)
	id, isNew := g.getID(fullName, true)
	if !isNew {
		return id
	}

	node := &Node{
		ID: id,

		Type:   NodeFunc,
		Object: fun,
	}

	if false { // TODO
		node.PID = g.addPkgNode(fun.Pkg())
	}

	if last := strings.LastIndex(funcName, "."); last >= 0 {
		// node.Label = funcName[last:]
		node.Label = funcName
	} else {
		node.Label = funcName
	}

	sig := fun.Type().(*types.Signature)
	if recv := sig.Recv(); recv != nil {
		if false { // TODO
			node.PID = g.addRecvTypeNode(recv)
		}
	}

	inGoRoot := func(pkg *types.Package) bool {
		if pkg == nil {
			return true
		}
		buildPkg, _ := build.Import(g.pkgPath(pkg), "", 0)
		return buildPkg.Goroot
	}

	if inGoRoot(fun.Pkg()) {
		node.Attrs = append(node.Attrs, "go_root")
	}
	if fun.Parent() == nil {
		node.Attrs = append(node.Attrs, "global")
	}

	g.Nodes[id] = node
	return id
}

func (g *StaticCallGraph) addDynamicEdge(callExpr *ast.CallExpr, caller *types.Func, callee *types.Var) ID {
	return "" // TODO
}

func (g *StaticCallGraph) addBuiltinEdge(callExpr *ast.CallExpr, caller *types.Func, callee *types.Builtin) ID {
	return "" // TODO
}

func (g *StaticCallGraph) addStaticEdge(callExpr *ast.CallExpr, caller, callee *types.Func) ID {
	// fullName := fmt.Sprintf("call @%d ~ %s -> %s",
	// 	callExpr.Pos(), normalize(caller.FullName()), normalize(callee.FullName()))

	// rm Pos, only addOnce
	fullName := fmt.Sprintf("call %s -> %s", g.funName(caller), g.funName(callee))
	id, isNew := g.getID(fullName, true)
	if !isNew {
		return id
	}

	callerId := g.addNode(caller)
	calleeId := g.addNode(callee)
	cEdge := &Edge{
		ID:     id,
		Source: callerId,
		Target: calleeId,
		Attrs:  []string{"static"}, // todo go / defer / closure call ?
	}

	g.Edges[id] = cEdge
	return id
}

func (g *StaticCallGraph) addRecvTypeNode(recv *types.Var) ID {
	pkg := recv.Pkg()
	tyStr := recv.Type().String()

	fullName := fmt.Sprintf("recv ~ %s ~ %s", g.pkgPath(pkg), tyStr)
	id, isNew := g.getID(fullName, true)
	if !isNew {
		return id
	}

	node := &Node{
		ID:  id,
		PID: g.addPkgNode(recv.Pkg()),

		Type:   NodeRecv,
		Object: recv,
	}

	if last := strings.LastIndex(tyStr, "."); last >= 0 {
		node.Label = tyStr[last+1:]
	} else {
		node.Label = tyStr
	}

	node.Attrs = append(node.Attrs, "type")
	if recv.Embedded() {
		node.Attrs = append(node.Attrs, "embedded")
	}
	if recv.IsField() {
		node.Attrs = append(node.Attrs, "field")
	}

	g.Nodes[id] = node
	return id
}

func (g *StaticCallGraph) addPkgNode(pkg *types.Package) ID {
	if pkg == nil {
		return ""
	}
	pkgPath := g.pkgPath(pkg)

	fullName := fmt.Sprintf("pkg ~ %s", pkgPath)
	id, isNew := g.getID(fullName, true)
	if !isNew {
		return id
	}

	path := pkgPath
	node := &Node{
		ID:    id,
		Label: pkg.Name(),
		Desc:  path,
		Attrs: []string{"package"},

		Type:   NodePkg,
		Object: pkg,
	}
	g.Nodes[id] = node
	return id
}

func (g *StaticCallGraph) pkgPath(pkg *types.Package) string {
	if pkg == nil {
		return ""
	}
	if pkg.Path() == "command-line-arguments" {
		return ""
	}
	return pkg.Path()
}

func (g *StaticCallGraph) funName(fun *types.Func) string {
	return strings.ReplaceAll(fun.FullName(), "command-line-arguments.", "")
}
