package main

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"strconv"
	"strings"
)

// spannerLocalName returns the local name for "cloud.google.com/go/spanner"
// from the file's import declarations. Returns an empty string if the package
// is not imported.
func spannerLocalName(file *ast.File) string {
	for _, imp := range file.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		if path != "cloud.google.com/go/spanner" {
			continue
		}
		if imp.Name != nil {
			return imp.Name.Name
		}
		return "spanner"
	}
	return ""
}

// spannerStatementLit returns n if it is a spanner.Statement{...} composite
// literal, where spannerIdent is the local name of the spanner package.
func spannerStatementLit(n ast.Node, spannerIdent string) (*ast.CompositeLit, bool) {
	comp, ok := n.(*ast.CompositeLit)
	if !ok {
		return nil, false
	}
	sel, ok := comp.Type.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Statement" {
		return nil, false
	}
	ident, ok := sel.X.(*ast.Ident)
	if !ok || ident.Name != spannerIdent {
		return nil, false
	}
	return comp, true
}

// concatStringLits returns the value of a string literal, or of string
// literals joined with +. It returns an error naming what was found if expr
// contains anything else, such as a variable or a function call.
func concatStringLits(expr ast.Expr) (string, error) {
	switch e := expr.(type) {
	case *ast.BasicLit:
		if e.Kind == token.STRING {
			return strconv.Unquote(e.Value)
		}
	case *ast.BinaryExpr:
		if e.Op == token.ADD {
			x, err := concatStringLits(e.X)
			if err != nil {
				return "", err
			}
			y, err := concatStringLits(e.Y)
			if err != nil {
				return "", err
			}
			return x + y, nil
		}
	}
	return "", fmt.Errorf("found %s", describeExpr(expr))
}

func describeExpr(expr ast.Expr) string {
	switch expr.(type) {
	case *ast.Ident:
		return "variable " + types.ExprString(expr)
	case *ast.CallExpr:
		return "function call " + types.ExprString(expr)
	}
	return "expression " + types.ExprString(expr)
}
