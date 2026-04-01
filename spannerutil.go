package main

import (
	"go/ast"
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
