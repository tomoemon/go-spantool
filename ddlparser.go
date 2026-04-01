package main

import (
	"fmt"

	"github.com/cloudspannerecosystem/memefish"
	"github.com/cloudspannerecosystem/memefish/ast"
)

type Schema struct {
	Tables map[string]*Table
}

type Table struct {
	Name        string
	Columns     []*Column
	PrimaryKeys []string
}

type Column struct {
	Name       string
	NotNull    bool
	Generated  bool
	HasDefault bool
}

func (t *Table) ColumnByName(name string) *Column {
	for _, c := range t.Columns {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func (t *Table) RequiredColumns() []string {
	var cols []string
	for _, c := range t.Columns {
		if c.NotNull && !c.Generated && !c.HasDefault {
			cols = append(cols, c.Name)
		}
	}
	return cols
}

func ParseDDL(src string) (*Schema, error) {
	schema := &Schema{Tables: make(map[string]*Table)}

	ddls, err := memefish.ParseDDLs("", src)
	if err != nil {
		return nil, fmt.Errorf("parsing DDL: %w", err)
	}

	for _, ddl := range ddls {
		ct, ok := ddl.(*ast.CreateTable)
		if !ok {
			continue
		}
		table := &Table{
			Name: ct.Name.Idents[len(ct.Name.Idents)-1].Name,
		}
		for _, col := range ct.Columns {
			c := &Column{
				Name:    col.Name.Name,
				NotNull: col.NotNull,
			}
			switch col.DefaultSemantics.(type) {
			case *ast.ColumnDefaultExpr:
				c.HasDefault = true
			case *ast.GeneratedColumnExpr:
				c.Generated = true
			}
			table.Columns = append(table.Columns, c)
		}
		for _, pk := range ct.PrimaryKeys {
			table.PrimaryKeys = append(table.PrimaryKeys, pk.Name.Name)
		}
		schema.Tables[table.Name] = table
	}

	if len(schema.Tables) == 0 {
		return nil, fmt.Errorf("no CREATE TABLE statements found in DDL")
	}
	return schema, nil
}
