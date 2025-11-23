package model

import (
	"go/ast"
	"reflect"
)

type RawField struct {
	Name       string        // Go identifier
	Comment    string        // top‐of‐field comment
	TypeExpr   ast.Expr      // AST for the type (pointer, slice, selector, …)
	TagLit     *ast.BasicLit // the raw `\`…\`` literal
	IsExport   bool          // ast.IsExported(Name)
	IsEmbedded bool
}

type RawStruct struct {
	Name       string // type name
	Alias      *string
	AliasPtr   *bool
	Comment    string
	TypeParams []string
	Fields     []*RawField
	PkgPath    string    // e.g. "github.com/you/project/model"
	File       *ast.File // to lookup imports for printing
}

type TypeRef struct {
	PkgPath    string // "" for builtins
	Name       string // "string", "UUID", "MyType"
	IsPtr      bool
	IsSlice    bool
	IsEmbedded bool
	Elem       *TypeRef // for Ptr or Slice
}

type ApiField struct {
	Name       string // exported Go name
	Type       *TypeRef
	Tag        reflect.StructTag // built from key→value map
	RawTag     reflect.StructTag // <-- ADD THIS
	Comment    string
	Omit       bool // user‐configurable omit
	IsEmbedded bool
}

type ApiStruct struct {
	Name     string
	Alias    *string
	AliasPtr *bool
	Comment  string
	Fields   []*ApiField
	Imports  map[string]bool // set of imports needed
	PkgName  string          // e.g. "api_v1"
}
