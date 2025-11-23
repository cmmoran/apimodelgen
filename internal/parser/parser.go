package parser

import (
	"fmt"
	"go/ast"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/jinzhu/inflection"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
	"golang.org/x/tools/go/packages"

	"github.com/cmmoran/apimodelgen/internal/model"
)

type ExternalAlias struct {
	PkgPath  string
	TypeName string
	TypeArgs []ast.Expr
}

// Parser holds state/results of a parse run.
type Parser struct {
	Opts Options

	Imports    map[string]*ImportMeta
	ApiImports map[string]*ImportMeta

	aliasCount      map[string]int
	RawStructs      RawStructs
	ApiStructs      ApiStructs
	externalAliases map[string]ExternalAlias

	// extPkgs caches on-disk parses and extracted StructTypes
	extPkgs   map[string]*externalPkg
	importMap map[string]string
}

// externalPkg is the cache entry for a single imported package.
type externalPkg struct {
	files         map[string]*ast.File          // filename → AST
	typToFile     map[*ast.StructType]*ast.File // struct → file
	structs       map[string]*ast.StructType    // typeName → struct AST
	typeAliases   map[string]ast.Expr           // alias name → aliased type expr (e.g. Time = time.Time)
	importAliases map[string]string             // import alias → import path (for that external package)
}

type RawStructs []*model.RawStruct

func (x RawStructs) Find(name string) *model.RawStruct {
	for _, s := range x {
		if s.Name == name {
			return s
		}
	}
	return nil
}

type ApiStructs []*model.ApiStruct

func (x ApiStructs) Find(name string) *model.ApiStruct {
	for _, s := range x {
		if s.Name == name {
			return s
		}
	}
	return nil
}

func (x ApiStructs) Len() int {
	return len(x)
}

func (x ApiStructs) Less(i, j int) bool {
	return x[i].Name < x[j].Name
}

func (x ApiStructs) Swap(i, j int) {
	x[i], x[j] = x[j], x[i]
}

// New executes the parser with opts.
func New(opts ...Option) (*Parser, error) {
	o := &Options{
		FlattenEmbedded: true,
	}
	for _, fn := range opts {
		fn(o)
	}

	return NewWithOpts(o)
}

func NewWithOpts(opts *Options) (*Parser, error) {
	opts.Normalize()

	p := &Parser{
		Opts:            *opts,
		Imports:         make(map[string]*ImportMeta),
		ApiImports:      make(map[string]*ImportMeta),
		aliasCount:      make(map[string]int),
		RawStructs:      make([]*model.RawStruct, 0),
		ApiStructs:      make([]*model.ApiStruct, 0),
		externalAliases: make(map[string]ExternalAlias),
		extPkgs:         make(map[string]*externalPkg),
	}

	return p, nil
}

func (p *Parser) BuildWorkingModel() []*model.WorkingType {
	b := NewBuilder(
		&p.Opts,
		p.RawStructs,
		p.Imports,
		p,
	)
	return b.BuildAll()
}

func (p *Parser) Parse() error {
	var (
		pkgs []*packages.Package
		err  error
	)
	pkgs, err = packages.Load(&packages.Config{
		Mode: packages.LoadImports | packages.LoadAllSyntax,
		Dir:  p.Opts.InDir,
		Fset: token.NewFileSet(),
	}, "./...")

	if err != nil {
		return err
	}
	if err = p.buildImportMap(); err != nil {
		return err
	}
	for _, pkg := range pkgs {
		for _, file := range pkg.Syntax {
			p.collectImports(file)
			p.collectStructs(pkg.PkgPath, file)
		}
	}
	wts := p.BuildWorkingModel()
	p.ApiStructs = ToApiStructs(wts, &p.Opts)
	// Build Patch structs (Xxx + PatchSuffix) from DTO ApiStructs.
	p.buildPatchStructs()

	p.populateApiImports()

	return nil
}

// buildPatchStructs synthesizes "patch" ApiStructs for each DTO ApiStruct.
// For a base DTO type Name, it creates Name + PatchSuffix, with field types:
//
//   - Slice / slice-alias fields → *PatchSlice[ElemPatch]
//   - All other fields           → pointerized scalar (via pointerizeTypeRef)
//
// This function assumes p.ApiStructs already contains only "base" DTO structs
// and alias types produced by ToApiStructs.
func (p *Parser) buildPatchStructs() {
	patchSuffix := p.Opts.PatchSuffix
	if patchSuffix == "" {
		patchSuffix = "Patch"
	}

	// Snapshot current ApiStructs so we don't iterate over the ones we append.
	baseStructs := make([]*model.ApiStruct, 0, len(p.ApiStructs))
	for _, api := range p.ApiStructs {
		if api == nil {
			continue
		}
		// Skip alias types (they are slice aliases, not DTOs).
		if api.Alias != nil {
			continue
		}
		// Skip anything that already looks like a Patch type.
		if strings.HasSuffix(api.Name, patchSuffix) {
			continue
		}
		baseStructs = append(baseStructs, api)
	}

	for _, base := range baseStructs {
		patchName := base.Name + patchSuffix

		// Avoid duplicate patch types if built multiple times.
		if p.ApiStructs.Find(patchName) != nil {
			continue
		}

		// Skip if base struct has no fields (already excluded upstream)
		if len(base.Fields) == 0 {
			continue
		}

		// Skip explicitly excluded types (Option.ExcludeTypes)
		if len(p.Opts.ExcludeTypes) > 0 {
			n := base.Name
			// Remove suffix when matching exclusions
			if len(p.Opts.Suffix) > 0 {
				n = strings.TrimSuffix(base.Name, p.Opts.Suffix)
			}
			for _, ex := range p.Opts.ExcludeTypes {
				if strings.EqualFold(ex, n) {
					continue
				}
			}
		}

		// Skip deprecated types when ExcludeDeprecated is active
		if p.Opts.ExcludeDeprecated &&
			strings.Contains(strings.ToLower(base.Comment), "deprecated") {
			continue
		}

		patch := &model.ApiStruct{
			Name:     patchName,
			Alias:    nil,
			AliasPtr: nil,
			Comment:  base.Comment,
			Fields:   make([]*model.ApiField, 0, len(base.Fields)),
			Imports:  make(map[string]bool),
			PkgName:  base.PkgName,
		}

		for _, f := range base.Fields {
			if f == nil || f.Omit {
				continue
			}

			pf := &model.ApiField{
				Name:       f.Name,
				Comment:    f.Comment,
				Tag:        f.Tag,
				Omit:       false,
				IsEmbedded: f.IsEmbedded,
			}

			// Rule: read-only or create-only → do NOT pointerize, do NOT PatchSlice
			if p.isGormReadOnly(f.RawTag) {
				// Use original concrete type, exactly as in DTO
				pf.Type = f.Type
			} else if f.IsEmbedded {
				// Embedded fields should point at the PATCH version of the embedded type
				pf.Type = p.pointerizePatchStructType(f.Type)
			} else {
				// Normal behavior
				pf.Type = p.buildPatchSliceFieldType(f.Type)
			}

			// Track imports required by the patch field type.
			trackImportsFromTypeRef(patch.Imports, pf.Type)

			patch.Fields = append(patch.Fields, pf)
		}

		p.ApiStructs = append(p.ApiStructs, patch)
	}
}

func (p *Parser) populateApiImports() {
	p.ApiImports = make(map[string]*ImportMeta)

	for _, api := range p.ApiStructs {
		for path := range api.Imports {
			for alias, meta := range p.Imports {
				if meta.Path == path && !meta.Mod {
					p.ApiImports[alias] = meta
				}
			}
		}
	}
}

func (p *Parser) collectImports(file *ast.File) {
	for _, imp := range file.Imports {
		path := strings.Trim(imp.Path.Value, `\"`)
		base := filepath.Base(path)
		alias := base
		if imp.Name != nil && imp.Name.Name != "_" && imp.Name.Name != "." {
			alias = imp.Name.Name
		}
		if p.aliasExists(alias) {
			continue
		}
		// ensure uniqueness
		count := p.aliasCount[alias]
		if count > 0 {
			alias = fmt.Sprintf("%s%d", alias, count+1)
		}
		p.aliasCount[alias]++
		p.Imports[alias] = &ImportMeta{
			Path:  path,
			Name:  base,
			Alias: alias,
		}
	}
}

func (p *Parser) collectStructs(pkgPath string, file *ast.File) {
	for _, decl := range file.Decls {

		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}

		genComment := commentText(gen.Doc)

		for _, spec := range gen.Specs {

			ts, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}

			// Skip true aliases: type X = Y
			if ts.Assign.IsValid() {
				continue
			}

			// Accumulate type-level comments
			typeComment := genComment
			if ts.Doc != nil {
				docTxt := commentText(ts.Doc)
				if docTxt != "" {
					if typeComment == "" {
						typeComment = docTxt
					} else {
						typeComment += "\n" + docTxt
					}
				}
			}

			// Deprecation-based exclusion
			if p.Opts.ExcludeDeprecated &&
				(strings.Contains(typeComment, "Deprecated") || strings.Contains(typeComment, "deprecated")) {
				p.Opts.ExcludeTypes = append(p.Opts.ExcludeTypes, strings.ToLower(ts.Name.Name))
			}

			// -----------------------------------------------------------------
			// 1. GENERIC ALIAS TYPES (IndexExpr / IndexListExpr)
			//    type MutableModel   model.MutableModel[uuid.UUID]
			//    type AuditModel     model.AuditModel[T]
			// -----------------------------------------------------------------
			switch rhs := ts.Type.(type) {

			case *ast.IndexExpr:
				// model.MutableModel[uuid.UUID]
				if sel, ok := rhs.X.(*ast.SelectorExpr); ok {
					if pkgIdent, ok := sel.X.(*ast.Ident); ok {
						aliasName := ts.Name.Name // "MutableModel"
						pkgAlias := pkgIdent.Name // "model"
						typeName := sel.Sel.Name  // "MutableModel"

						if meta, ok := p.Imports[pkgAlias]; ok {
							p.externalAliases[aliasName] = ExternalAlias{
								PkgPath:  meta.Path,
								TypeName: typeName,
								TypeArgs: []ast.Expr{rhs.Index}, // single type arg
							}
						}
						// Do NOT create RawStruct for this alias.
						continue
					}
				}

			case *ast.IndexListExpr:
				// model.SomeGeneric[A, B, ...]
				if sel, ok := rhs.X.(*ast.SelectorExpr); ok {
					if pkgIdent, ok := sel.X.(*ast.Ident); ok {
						aliasName := ts.Name.Name
						pkgAlias := pkgIdent.Name
						typeName := sel.Sel.Name

						if meta, ok := p.Imports[pkgAlias]; ok {
							args := make([]ast.Expr, len(rhs.Indices))
							copy(args, rhs.Indices)
							p.externalAliases[aliasName] = ExternalAlias{
								PkgPath:  meta.Path,
								TypeName: typeName,
								TypeArgs: args,
							}
						}
						continue
					}
				}
			}

			// -----------------------------------------------------------------
			// 2. SLICE ALIAS TYPES
			//    type Widgets []*Widget
			// -----------------------------------------------------------------
			if at, ok := ts.Type.(*ast.ArrayType); ok {
				var (
					aliasName string
					isPtr     bool
				)

				switch elt := at.Elt.(type) {
				case *ast.Ident:
					aliasName = elt.Name
					isPtr = false
				case *ast.StarExpr:
					if id, ok := elt.X.(*ast.Ident); ok {
						aliasName = id.Name
						isPtr = true
					}
				}

				if aliasName != "" {
					p.RawStructs = append(p.RawStructs, &model.RawStruct{
						Name:     ts.Name.Name,
						Alias:    &aliasName,
						AliasPtr: &isPtr,
						Comment:  typeComment,
						Fields:   []*model.RawField{},
						PkgPath:  pkgPath,
						File:     file,
					})
				}
				continue
			}

			// -----------------------------------------------------------------
			// 3. REAL STRUCT TYPES
			//    type Widget struct { ... }
			// -----------------------------------------------------------------
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				// Not a struct, not a slice alias, not a generic alias.
				continue
			}

			raw := &model.RawStruct{
				Name:    ts.Name.Name,
				Comment: typeComment,
				TypeParams: func() []string {
					if ts.TypeParams == nil {
						return nil
					}
					out := make([]string, len(ts.TypeParams.List))
					for i, fp := range ts.TypeParams.List {
						out[i] = fp.Names[0].Name
					}
					return out
				}(),
				Fields:  []*model.RawField{},
				PkgPath: pkgPath,
				File:    file,
			}

			// parse fields
			for _, fld := range st.Fields.List {
				flds := p.parseRawFields(fld)
				raw.Fields = append(raw.Fields, flds...)
			}

			p.RawStructs = append(p.RawStructs, raw)
		}
	}
}

func (p *Parser) rawFieldsFromExternalAST(pkgPath string, file *ast.File, st *ast.StructType) []*model.RawField {
	var raws []*model.RawField

	for _, fld := range st.Fields.List {
		comment := commentText(fld.Comment)
		docTxt := commentText(fld.Doc)

		if p.Opts.ExcludeDeprecated &&
			(strings.Contains(comment, "Deprecated") || strings.Contains(docTxt, "Deprecated")) {
			continue
		}

		tagLit := fld.Tag
		expr := fld.Type

		// -------- Alias resolution for external fields --------
		switch t := expr.(type) {

		case *ast.Ident:
			// e.g. Time -> time.Time (via type alias)
			if aliased, ok := p.resolveExternalAlias(pkgPath, t.Name); ok {
				expr = aliased
			}

		case *ast.StarExpr:
			// e.g. *Time
			if id, ok := t.X.(*ast.Ident); ok {
				if aliased, ok := p.resolveExternalAlias(pkgPath, id.Name); ok {
					expr = &ast.StarExpr{
						Star: t.Star,
						X:    aliased,
					}
				}
			}

		case *ast.IndexExpr:
			// e.g. MutableModel[T]
			if id, ok := t.X.(*ast.Ident); ok {
				if aliased, ok := p.resolveExternalAlias(pkgPath, id.Name); ok {
					expr = &ast.IndexExpr{
						X:      aliased,
						Lbrack: t.Lbrack,
						Index:  t.Index,
						Rbrack: t.Rbrack,
					}
				}
			}

		case *ast.IndexListExpr:
			// e.g. MutableModel[T, U]
			if id, ok := t.X.(*ast.Ident); ok {
				if aliased, ok := p.resolveExternalAlias(pkgPath, id.Name); ok {
					expr = &ast.IndexListExpr{
						X:       aliased,
						Lbrack:  t.Lbrack,
						Indices: t.Indices,
						Rbrack:  t.Rbrack,
					}
				}
			}
		}

		// -------- Named vs embedded fields --------

		// Embedded field
		if len(fld.Names) == 0 {
			raws = append(raws, &model.RawField{
				Name:       "", // embedded indicator
				Comment:    comment,
				TypeExpr:   expr,
				TagLit:     tagLit,
				IsExport:   false,
				IsEmbedded: true,
			})
			continue
		}

		// Named fields
		for _, id := range fld.Names {
			raws = append(raws, &model.RawField{
				Name:       id.Name,
				Comment:    comment,
				TypeExpr:   expr,
				TagLit:     tagLit,
				IsExport:   ast.IsExported(id.Name),
				IsEmbedded: false,
			})
		}
	}

	return raws
}

func (p *Parser) resolveExternalAlias(pkgPath, aliasName string) (ast.Expr, bool) {
	if p.extPkgs == nil {
		return nil, false
	}

	ep, ok := p.extPkgs[pkgPath]
	if !ok || ep == nil || ep.typeAliases == nil {
		return nil, false
	}

	expr, ok := ep.typeAliases[aliasName]
	if !ok || expr == nil {
		return nil, false
	}

	// The aliased expression is already a valid ast.Expr (Ident, SelectorExpr,
	// IndexExpr, IndexListExpr, etc.). We just return it as-is.
	return expr, true
}

func (p *Parser) resolveSliceElemDTOName(t *model.TypeRef) (elemName string, elemPkg string, ok bool) {
	if t == nil {
		return "", "", false
	}

	// CASE 1: direct slice    ([]T or []*T)
	if t.IsSlice && t.Elem != nil {
		raw := t.Elem

		name := raw.Name
		pkg := raw.PkgPath

		// If it's already a Patch type, strip PatchSuffix
		if p.Opts.PatchSuffix != "" && strings.HasSuffix(name, p.Opts.PatchSuffix) {
			name = strings.TrimSuffix(name, p.Opts.PatchSuffix)
		}

		// Resolve to DTO name if necessary
		if p.Opts.Suffix != "" && !strings.HasSuffix(name, p.Opts.Suffix) {
			name = p.resolveName(name)
		}

		return name, pkg, true
	}

	// CASE 2: alias slice type (AgencyContacts)
	if api := p.ApiStructs.Find(t.Name); api != nil && api.Alias != nil {
		name := *api.Alias // e.g. AgencyContactDTO

		// If Patch suffix present, strip
		if p.Opts.PatchSuffix != "" && strings.HasSuffix(name, p.Opts.PatchSuffix) {
			name = strings.TrimSuffix(name, p.Opts.PatchSuffix)
		}

		// Ensure name includes Suffix
		if p.Opts.Suffix != "" && !strings.HasSuffix(name, p.Opts.Suffix) {
			name = p.resolveName(name)
		}

		return name, "", true
	}

	return "", "", false
}

// resolveAliasSliceElem returns the element type when t.Name refers to an alias
// whose WorkingType is KindAlias → KindSlice → Elem.
func (p *Parser) resolveAliasSliceElem(t *model.TypeRef) *model.TypeRef {
	if t == nil || t.Name == "" {
		return nil
	}

	// Look up WorkingType by name
	wts := p.BuildWorkingModel()

	for _, wt := range wts {
		if wt.Name == t.Name && wt.Kind == model.KindAlias && wt.Underlying != nil {
			// Must be slice alias
			if wt.Underlying.Kind == model.KindSlice && wt.Underlying.Underlying != nil {
				return workingTypeToTypeRef(wt.Underlying.Underlying)
			}
		}
	}
	return nil
}

func pointerizeTypeRef(t *model.TypeRef) *model.TypeRef {
	if t == nil {
		return nil
	}

	// For PATCH semantics we always add one level of indirection to the
	// original type reference. This lets us distinguish three states:
	//   nil      -> field not touched
	//   &nil     -> explicit clear (for pointer fields)
	//   &value   -> explicit set/update
	//
	// Represent this by wrapping the original TypeRef in a new pointer
	// TypeRef. The inner TypeRef (t) retains its existing pointer/slice
	// information so typeExprToJen can render *T, **T, []T, etc.
	return &model.TypeRef{
		IsPtr: true,
		Elem:  t,
	}
}

// pointerizePatchStructType clones the provided TypeRef and returns a pointer
// to the PATCH version of that struct (Foo → *FooPatch). Pointer/slice metadata
// from the original TypeRef is preserved inside the returned pointer wrapper.
func (p *Parser) pointerizePatchStructType(t *model.TypeRef) *model.TypeRef {
	if t == nil {
		return nil
	}

	clone := cloneTypeRef(t)
	leaf := clone
	for leaf != nil && leaf.Elem != nil {
		leaf = leaf.Elem
	}
	if leaf != nil && !strings.HasSuffix(leaf.Name, p.Opts.PatchSuffix) {
		leaf.Name = leaf.Name + p.Opts.PatchSuffix
	}

	return pointerizeTypeRef(clone)
}

// cloneTypeRef deep-copies a TypeRef graph.
func cloneTypeRef(t *model.TypeRef) *model.TypeRef {
	if t == nil {
		return nil
	}

	clone := &model.TypeRef{
		Name:    t.Name,
		PkgPath: t.PkgPath,
		IsPtr:   t.IsPtr,
		IsSlice: t.IsSlice,
	}
	if t.Elem != nil {
		clone.Elem = cloneTypeRef(t.Elem)
	}

	return clone
}

func (p *Parser) buildPatchSliceFieldType(t *model.TypeRef) *model.TypeRef {
	if t == nil {
		return nil
	}

	var baseElem *model.TypeRef

	// Slice or alias-to-slice detection
	switch {
	case t.IsSlice && t.Elem != nil:
		baseElem = t.Elem

	case !t.IsSlice:
		// WorkingType alias → slice
		if aliasElem := p.resolveAliasSliceElem(t); aliasElem != nil {
			baseElem = aliasElem
			break
		}
		// RawStruct alias fallback
		if raw := p.RawStructs.Find(t.Name); raw != nil && raw.Alias != nil {
			baseElem = &model.TypeRef{Name: *raw.Alias}
		}
	}

	// Not a slice → scalar pointer semantics
	if baseElem == nil {
		return pointerizeTypeRef(t)
	}

	// ------------------------------------------------------------
	// PRESERVE POINTER-NESS OF THE ELEMENT
	// ------------------------------------------------------------
	elemPtr := baseElem.IsPtr

	// The *base* for naming is the underlying element type
	underlying := baseElem
	if baseElem.IsPtr && baseElem.Elem != nil {
		underlying = baseElem.Elem
	}

	// Apply DTO suffix if needed
	elemName := underlying.Name
	if p.Opts.Suffix != "" && !strings.HasSuffix(elemName, p.Opts.Suffix) {
		elemName = p.resolveName(elemName)
	}

	// Build name of the patch-element type
	elemPatchName := elemName + p.Opts.PatchSuffix

	// Build TypeRef for the element INSIDE PatchSlice, preserving ptr-ness
	var elemRef *model.TypeRef
	if elemPtr {
		elemRef = &model.TypeRef{
			Name:  elemPatchName,
			IsPtr: true,
			Elem: &model.TypeRef{
				Name: elemPatchName,
			},
		}
	} else {
		elemRef = &model.TypeRef{
			Name: elemPatchName,
		}
	}

	// ------------------------------------------------------------
	// FINALLY RETURN *PatchSlice[ElemRef]
	// ------------------------------------------------------------
	return &model.TypeRef{
		Name:  "PatchSlice",
		IsPtr: true,
		Elem:  elemRef,
	}
}

// ResolveWorkingType returns the WorkingType if it exists in the parsed model.
func (p *Parser) ResolveWorkingType(name string) (*model.WorkingType, bool) {
	for _, wt := range p.BuildWorkingModel() {
		if wt.Name == name {
			return wt, true
		}
	}
	return nil, false
}

func (p *Parser) parseRawFields(f *ast.Field) []*model.RawField {
	if f == nil {
		return nil
	}

	var out []*model.RawField

	// Names: one field spec may contain multiple names:
	//   X, Y string
	if len(f.Names) == 0 {
		// Embedded field: the name comes from the type expression.
		// e.g., `AuditModel` or `*AuditModel`
		name := embeddedFieldName(f.Type)
		out = append(out, &model.RawField{
			Name:       name,
			IsEmbedded: true,
			TypeExpr:   f.Type,
			TagLit:     f.Tag,
			Comment:    commentText(f.Comment),
		})
		return out
	}

	for _, id := range f.Names {
		out = append(out, &model.RawField{
			Name:       id.Name,
			IsEmbedded: false,
			TypeExpr:   f.Type,
			TagLit:     f.Tag,
			Comment:    commentText(f.Comment),
		})
	}

	return out
}

// helpers
func embeddedFieldName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.SelectorExpr:
		return t.Sel.Name
	case *ast.StarExpr:
		return embeddedFieldName(t.X)
	}
	return ""
}

// rawFieldsFromAST converts an *ast.StructType into your RawFields
func (p *Parser) rawFieldsFromAST(st *ast.StructType) []*model.RawField {
	var raws []*model.RawField
	comment := "" // you can pull comments from st.Fields.List[i].Comment if needed
	for _, fld := range st.Fields.List {
		comment = commentText(fld.Comment)
		docTxt := commentText(fld.Doc)
		if p.Opts.ExcludeDeprecated && (strings.Contains(comment, "Deprecated") || strings.Contains(docTxt, "Deprecated")) {
			continue
		}
		tagLit := fld.Tag
		if len(fld.Names) == 0 {
			// embedded
			raws = append(raws, &model.RawField{
				Name:       "",
				Comment:    comment,
				TypeExpr:   fld.Type,
				TagLit:     tagLit,
				IsExport:   false,
				IsEmbedded: true,
			})
		} else {
			// named
			for _, id := range fld.Names {
				raws = append(raws, &model.RawField{
					Name:       id.Name,
					Comment:    comment,
					TypeExpr:   fld.Type,
					TagLit:     tagLit,
					IsExport:   ast.IsExported(id.Name),
					IsEmbedded: false,
				})
			}
		}
	}
	return raws
}

// buildTagLiteral serializes a key->value map into a struct tag literal
func buildTagLiteral(m map[string]string) string {
	parts := make([]string, 0)
	for k, v := range m {
		parts = append(parts, fmt.Sprintf("%s:\"%s\"", k, v))
	}
	s := strings.Join(parts, " ")
	return fmt.Sprintf("`%s`", s)
}

func (p *Parser) aliasExists(a string) bool {
	for _, m := range p.Imports {
		if m.Alias == a && !m.Mod {
			return true
		}
	}
	return false
}

func (p *Parser) pluralize(s string) string {
	if inflection.Singular(s) == s {
		return p.resolveName(inflection.Plural(s))
	}
	return p.resolveName(s)
}

func (p *Parser) contains(name string) (rawContains bool, apiContains bool) {
	for _, d := range p.RawStructs {
		if d.Name == name {
			rawContains = true
			break
		}
	}
	for _, d := range p.ApiStructs {
		if d.Name == name {
			apiContains = true
			break
		}
	}
	return
}

func (p *Parser) resolveName(name string) string {
	if p.Opts.Suffix != "" && !strings.HasSuffix(name, p.Opts.Suffix) {
		return name + p.Opts.Suffix
	}
	return name
}

func commentText(cg *ast.CommentGroup) string {
	if cg == nil {
		return ""
	}
	var b strings.Builder
	for _, c := range cg.List {
		txt := strings.TrimSpace(strings.Trim(strings.TrimPrefix(strings.TrimPrefix(c.Text, "//"), "/*"), "*/"))
		b.WriteString(txt)
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

func (p *Parser) tagExcluded(tag string) bool {
	if tag == "" || len(p.Opts.ExcludeByTags) == 0 {
		return false
	}
	st := reflect.StructTag(tag)
	for _, f := range p.Opts.ExcludeByTags {
		if v, ok := st.Lookup(f.Key); ok {
			for _, part := range strings.Split(v, ";") {
				if part == f.Value {
					return true
				}
			}
		}
	}
	return false
}

// findGoModDir walks up from cwd until it finds go.mod.
func (p *Parser) findGoModDir() (string, error) {
	var (
		err  error
		from = p.Opts.InDir
	)
	if err != nil {
		return "", err
	}
	for {
		if _, err = os.Stat(filepath.Join(from, "go.mod")); err == nil {
			return from, nil
		}
		parent := filepath.Dir(from)
		if parent == from {
			return "", fmt.Errorf("no go.mod found")
		}
		from = parent
	}
}

// findGoCache walks up from cwd until it finds go.mod.
func (p *Parser) findGoCache() (string, error) {
	var (
		err  error
		from = p.Opts.InDir
		fi   os.FileInfo
	)
	if err != nil {
		return "", err
	}
	for {
		candidate := filepath.Join(from, "go-cache")
		if fi, err = os.Stat(candidate); err == nil && fi.IsDir() {
			return candidate, nil
		}
		parent := filepath.Dir(from)
		if parent == from {
			return "", fmt.Errorf("no go-cache found")
		}
		from = parent
	}
}

// parseRequires parses all “require” and “replace” directives.
func parseRequires(modDir string) ([]module.Version, []module.Version, error) {
	data, err := os.ReadFile(filepath.Join(modDir, "go.mod"))
	if err != nil {
		return nil, nil, err
	}
	mf, err := modfile.Parse("go.mod", data, nil)
	if err != nil {
		return nil, nil, err
	}
	reqs := make([]module.Version, 0, len(mf.Require))
	for _, r := range mf.Require {
		reqs = append(reqs, r.Mod)
	}
	reps := make([]module.Version, 0, len(mf.Replace))
	for _, r := range mf.Replace {
		// r.Old is the path@version being replaced
		// r.Normalize is the module (possibly a local dir or different version)
		reps = append(reps, module.Version{Path: r.New.Path, Version: r.New.Version})
	}
	return reqs, reps, nil
}

// moduleCacheDir returns $GOMODCACHE or $GOPATH/pkg/mod.
func (p *Parser) moduleCacheDir() (string, error) {
	if m, err := p.findGoCache(); err == nil {
		return filepath.Join(m, "pkg", "mod"), nil
	}
	if m := os.Getenv("GOMODCACHE"); m != "" {
		return m, nil
	}
	g := os.Getenv("GOPATH")
	if g == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		g = filepath.Join(home, "go")
	}
	return filepath.Join(g, "pkg", "mod"), nil
}

// buildImportMap constructs map[modulePath]filesystemDir.
func (p *Parser) buildImportMap() error {
	modDir, err := p.findGoModDir()
	if err != nil {
		return err
	}
	reqs, reps, err := parseRequires(modDir)
	if err != nil {
		return err
	}
	cache, err := p.moduleCacheDir()
	if err != nil {
		return err
	}

	m := make(map[string]string, len(reqs)+len(reps)+1)
	// the main module is the directory itself
	mainMod, err := os.ReadFile(filepath.Join(modDir, "go.mod"))
	if err == nil {
		if mf, mfErr := modfile.Parse("go.mod", mainMod, nil); mfErr == nil {
			m[mf.Module.Mod.Path] = modDir
		}
	}

	for _, v := range append(reqs, reps...) {
		// if v.Version is empty, assume a local replace
		if v.Version == "" {
			// probably a local replace; point at module directory
			m[v.Path] = filepath.Join(modDir, filepath.FromSlash(v.Path))
		} else {
			// standard module cache layout: path@version
			key := fmt.Sprintf("%s@%s", v.Path, v.Version)
			m[v.Path] = filepath.Join(cache, key)
		}
	}
	for k, v := range m {
		base := filepath.Base(k)
		p.Imports[k] = &ImportMeta{
			Path:  k,
			Name:  base,
			Alias: base,
			Dir:   v,
			Mod:   true,
		}
	}

	return nil
}

func (p *Parser) isExcludedBaseType(t *model.TypeRef) bool {
	if t == nil || len(p.Opts.ExcludeTypes) == 0 {
		return false
	}

	// Start from the type name
	name := t.Name

	// If it's a slice, use the element name
	if t.IsSlice && t.Elem != nil {
		name = t.Elem.Name
	} else {
		// If it's an alias type (e.g. AgencyContacts), try to resolve to its alias target
		if api := p.ApiStructs.Find(t.Name); api != nil && api.Alias != nil {
			name = *api.Alias // e.g. AgencyContactDTO
		}
	}

	// Strip DTO suffix if present so ExcludeTypes can be specified
	// using the original model name (e.g. "AgencyContact").
	if len(p.Opts.Suffix) > 0 && strings.HasSuffix(name, p.Opts.Suffix) {
		name = strings.TrimSuffix(name, p.Opts.Suffix)
	}

	for _, ex := range p.Opts.ExcludeTypes {
		if ex == name {
			return true
		}
	}
	return false
}

func (p *Parser) resolveUnderlyingStructName(t *model.TypeRef) (string, bool) {
	if t == nil {
		return "", false
	}

	// Start from the type's own name (ignores ptr/slice wrapper here).
	name := t.Name
	if name == "" {
		return "", false
	}

	visited := map[string]bool{}

	for {
		if visited[name] {
			break
		}
		visited[name] = true

		rs := p.RawStructs.Find(name)
		if rs == nil {
			break
		}

		// If this RawStruct has fields, it's a struct definition.
		if rs.Fields != nil && len(rs.Fields) > 0 {
			return rs.Name, true
		}

		// If it's a type alias, follow the alias chain.
		if rs.Alias != nil && *rs.Alias != name {
			name = *rs.Alias
			continue
		}

		break
	}

	// Try external struct if we have a package path.
	if t.PkgPath != "" {
		if _, st, err := p.getExternalStructAST(t.PkgPath, name); err == nil && st != nil {
			return name, true
		}
	}

	return "", false
}

func (p *Parser) isGormReadOnly(tag reflect.StructTag) bool {
	if tag == "" {
		return false
	}

	raw := tag.Get("gorm")
	if raw == "" {
		return false
	}

	parts := strings.Split(raw, ";")
	for _, part := range parts {
		part = strings.TrimSpace(part)

		// read-only cases:
		if part == "->" || part == "<-:create" {
			return true
		}

		// gorm primary key is typically immutable
		if part == "primaryKey" {
			return true
		}
	}

	return false
}
