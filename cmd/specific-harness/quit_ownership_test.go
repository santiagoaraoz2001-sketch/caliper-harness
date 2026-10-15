package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// Source guard only. Actual Model.Update/owned-process cleanup controls live in tui;
// the native terminal quit and memory receipts remain root's live gate.
func TestRunTUIHasNoGlobalResidentUnload(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "main.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "runTUI" {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok {
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
					if sel.Sel.Name == "UnloadOllama" {
						t.Error("quit performs allmodel resident unload")
					}
					if sel.Sel.Name == "CloseOwnedServing" {
						found = true
					}
				}
			}
			return true
		})
	}
	if !found {
		t.Fatal("quit lacks owned process cleanup")
	}
}
