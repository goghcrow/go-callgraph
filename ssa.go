package callgraph

import (
	"fmt"
	"os"

	"github.com/goghcrow/go-loader"
	"golang.org/x/tools/go/callgraph"
	"golang.org/x/tools/go/callgraph/cha"
	"golang.org/x/tools/go/callgraph/rta"
	"golang.org/x/tools/go/callgraph/static"
	"golang.org/x/tools/go/callgraph/vta"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

type Algo uint64

const (
	Static Algo = iota
	CHA
	RTA
	VTA
)

type SSACallGraph struct {
	Prog  *ssa.Program
	Pkgs  []*ssa.Package
	Mains []*ssa.Package
}

// NewSSA Notice loader.Flags.LoadDepts must true
func NewSSA(l *loader.Loader) *SSACallGraph {
	g := &SSACallGraph{}
	g.analysis(l.Init, l.Flags.PrintErrors)
	return g
}

func (c *SSACallGraph) analysis(initPkgs []*packages.Package, printErrors bool) {
	var (
		prog *ssa.Program
		init []*ssa.Package
	)

	mode := ssa.InstantiateGenerics
	prog, init = ssautil.AllPackages(initPkgs, mode)
	prog.Build()

	if printErrors {
		c.doPrintErrors(initPkgs, init)
	}

	c.Prog = prog
	c.Pkgs = prog.AllPackages()
	c.Mains = c.mainPackages()
}

func (c *SSACallGraph) doPrintErrors(initPkgs []*packages.Package, init []*ssa.Package) {
	for i, p := range init {
		if p == nil && initPkgs[i].Name != "" {
			errLog("Fail getting SSA for pkg: " + initPkgs[i].PkgPath)
		}
	}
}

func (c *SSACallGraph) mainPackages() (mains []*ssa.Package) {
	for _, p := range c.Pkgs {
		if p != nil && p.Pkg.Name() == "main" && p.Func("main") != nil {
			mains = append(mains, p)
		}
	}
	return
}

func (c *SSACallGraph) CallGraph(algo Algo) (cg *callgraph.Graph) {
	switch algo {
	case Static:
		cg = static.CallGraph(c.Prog)
	case CHA:
		cg = cha.CallGraph(c.Prog)
	case RTA:
		var roots []*ssa.Function
		for _, main := range c.Mains {
			roots = append(roots, main.Func("init"), main.Func("main"))
		}
		cg = rta.Analyze(roots, true).CallGraph
	case VTA:
		cg = vta.CallGraph(ssautil.AllFunctions(c.Prog), cha.CallGraph(c.Prog))
	default:
		panic("unsupported algo")
	}
	cg.DeleteSyntheticNodes()
	return
}

func errLog(a ...any) {
	_, _ = fmt.Fprintln(os.Stderr, a...)
}
