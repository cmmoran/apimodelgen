package model

import (
	"go/ast"
	"reflect"
)

type Kind int

const (
	KindInvalid Kind = iota
	KindBuiltin      // string, int, bool, etc.
	KindStruct       // real struct with fields
	KindAlias        // type MyName = OtherType
	KindPointer      // *T
	KindSlice        // []T
)

type WorkingType struct {
	// Identity ------------------------------------------------------------
	Name    string // "User", "AddressDTO"
	PkgPath string // import path, "" for local or builtin
	Kind    Kind

	// Structure ------------------------------------------------------------
	Underlying *WorkingType    // alias → its target; pointer → elem; slice → elem
	Fields     []*WorkingField // only valid when KindStruct
	Comment    string
	// Generic params and arguments (minimal)
	TypeParams []string   // for templates, e.g. ["T"]
	TypeArgs   []*TypeRef // for concrete instantiations, e.g. [uuid.UUID]
	// Metadata / Behavior --------------------------------------------------

	IsExternal   bool // came from external package
	IsDeprecated bool
	Omit         bool // excluded by option or tag
	Embedded     bool // this type was originally embedded in a struct

	// Transformation Flags -------------------------------------------------
	NameResolved bool // indicates suffix/pluralization has already been applied
	AliasApplied bool // indicates alias-flattening processed

	RawFile *ast.File
}

type WorkingField struct {
	// Identity -------------------------------------------------------------
	Name     string // final API field name
	RawName  string // original Go identifier
	Comment  string
	Embedded bool

	// Type -----------------------------------------------------------------
	Type *WorkingType

	// Tags -----------------------------------------------------------------
	Tag        reflect.StructTag
	RawTag     reflect.StructTag // before transformations
	Omit       bool
	Deprecated bool
}
