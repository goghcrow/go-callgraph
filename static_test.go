package callgraph

import (
	"go/types"
	"testing"

	astmatcher "github.com/goghcrow/go-ast-matcher"
	"github.com/goghcrow/go-loader"
	"github.com/goghcrow/go-matcher"
	. "github.com/goghcrow/go-matcher/combinator"
)

func TestExampleStaticCallGraph(t *testing.T) {
	l := loader.MustNew("./", loader.WithSuppressErrors())
	m := matcher.New()
	am := astmatcher.New(l, m)

	callExprPtn := FuncCalleeOf(m, func(_ *MatchCtx, callee *types.Func) bool {
		// return callee.Pkg() != nil && callee.Pkg().Name() == "callgraph"
		return true
	})
	graph := NewStatic(am, WithCallExprPattern(callExprPtn))
	graph.Dot().OpenOnline()
}
