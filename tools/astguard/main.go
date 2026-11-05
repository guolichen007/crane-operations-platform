// Command astguard enforces source-level call constraints that are not
// observable through HTTP behaviour alone. It runs from the repository root
// (repository-contract CI job) and never inside an isolated test binary.
//
// Rule: in services/cloud-api/internal/app (non-test files), the asset PATCH
// route must call asset.PatchAsset (presence-aware), and no file may call the
// whole-object asset.UpdateAsset wrapper, which would turn omitted keys into
// clears.
package main

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const assetPatchRoute = "PATCH /api/v1/assets/{id}"

type source struct {
	name string
	src  string
}

// checkAssetPatch applies the rule to a set of Go sources from one package.
func checkAssetPatch(files []source) error {
	fset := token.NewFileSet()
	var problems []string
	routeFound := false
	for _, f := range files {
		file, err := parser.ParseFile(fset, f.name, f.src, 0)
		if err != nil {
			return fmt.Errorf("parse %s: %w", f.name, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if isSelector(call.Fun, "asset", "UpdateAsset") {
				problems = append(problems, fmt.Sprintf("%s: calls asset.UpdateAsset (use asset.PatchAsset)", fset.Position(call.Pos())))
			}
			// api.HandleFunc("PATCH /api/v1/assets/{id}", func(...) { ... })
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "HandleFunc" && len(call.Args) == 2 {
				if lit, ok := call.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING && strings.Trim(lit.Value, "`\"") == assetPatchRoute {
					routeFound = true
					if !callsSelector(call.Args[1], "asset", "PatchAsset") {
						problems = append(problems, fmt.Sprintf("%s: %s handler does not call asset.PatchAsset", fset.Position(call.Pos()), assetPatchRoute))
					}
				}
			}
			return true
		})
	}
	if !routeFound {
		problems = append(problems, "route "+assetPatchRoute+" not registered")
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return errors.New(strings.Join(problems, "\n"))
	}
	return nil
}

func isSelector(e ast.Expr, pkg, name string) bool {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != name {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == pkg
}

func callsSelector(n ast.Node, pkg, name string) bool {
	found := false
	ast.Inspect(n, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok && isSelector(call.Fun, pkg, name) {
			found = true
		}
		return !found
	})
	return found
}

func loadPackage(dir string) ([]source, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		return nil, err
	}
	var out []source
	for _, m := range matches {
		if strings.HasSuffix(m, "_test.go") {
			continue
		}
		b, err := os.ReadFile(m)
		if err != nil {
			return nil, err
		}
		out = append(out, source{m, string(b)})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no Go sources in %s (run from the repository root)", dir)
	}
	return out, nil
}

func main() {
	files, err := loadPackage(filepath.Join("services", "cloud-api", "internal", "app"))
	if err == nil {
		err = checkAssetPatch(files)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "astguard:", err)
		os.Exit(1)
	}
	fmt.Println("astguard: asset PATCH route uses asset.PatchAsset; no asset.UpdateAsset callers")
}
