package parser

import (
	"fmt"
	"go/ast"
	"reflect"
	"strings"

	"github.com/jinzhu/inflection"

	"github.com/cmmoran/apimodelgen/internal/model"
)

// Builder constructs a normalized graph of WorkingType values from
// RawStructs + Options.
type Builder struct {
	parser  *Parser
	opts    *Options
	raws    RawStructs
	imports map[string]*ImportMeta

	byName    map[string]*model.WorkingType
	resolving map[string]bool
}

// NewBuilder initializes a Builder with options, raw structs, and imports.
func NewBuilder(
	opts *Options,
	raws RawStructs,
	imports map[string]*ImportMeta,
	parser *Parser,
) *Builder {
	return &Builder{
		parser:    parser,
		opts:      opts,
		raws:      raws,
		imports:   imports,
		byName:    make(map[string]*model.WorkingType),
		resolving: make(map[string]bool),
	}
}

// BuildAll is the main entrypoint:
//  1. Create WorkingType shells for each RawStruct.
//  2. Populate fields/aliases.
//  3. Apply all transformations.
//  4. Return all non-omitted WorkingTypes.
func (b *Builder) BuildAll() []*model.WorkingType {
	// 1) Create shells for all known raw structs.
	for _, raw := range b.raws {
		if raw == nil {
			continue
		}
		b.ensureWorkingType(raw.Name)
	}

	// 2) Populate fields / alias underlying types.
	for _, wt := range b.byName {
		b.populateFields(wt)
	}

	// 3) Apply transformations.
	for _, wt := range b.byName {
		b.applyTransformations(wt)
	}

	// 4) Collect non-omitted types.
	out := make([]*model.WorkingType, 0, len(b.byName))
	for _, wt := range b.byName {
		if wt == nil || wt.Omit {
			continue
		}
		out = append(out, wt)
	}
	return out
}

// ensureWorkingType returns an existing or newly created WorkingType shell
// for the given name.
func (b *Builder) ensureWorkingType(name string) *model.WorkingType {
	if t, ok := b.byName[name]; ok {
		return t
	}
	raw := b.raws.Find(name)
	wt := &model.WorkingType{
		Name:    name,
		PkgPath: "",
		Kind:    model.KindStruct,
		Fields:  []*model.WorkingField{},
	}
	if raw != nil {
		wt.PkgPath = raw.PkgPath
		wt.Comment = raw.Comment
		if raw.TypeParams != nil {
			wt.TypeParams = append([]string{}, raw.TypeParams...)
		}
	}
	b.byName[name] = wt
	return wt
}

// populateFields fills in the fields (or alias underlying type) for a WorkingType.
func (b *Builder) populateFields(wt *model.WorkingType) {
	if wt == nil {
		return
	}
	if b.resolving[wt.Name] {
		// cycle or already in progress; avoid infinite recursion
		return
	}
	b.resolving[wt.Name] = true
	defer delete(b.resolving, wt.Name)

	raw := b.raws.Find(wt.Name)
	if raw == nil {
		return
	}

	// Handle alias raw types: type X []T or type X []*T (already captured in RawStruct.Alias/AliasPtr).
	if raw.Alias != nil {
		wt.Kind = model.KindAlias
		wt.Underlying = b.resolveTypeExprAlias(*raw.Alias, raw.AliasPtr)
		return
	}

	// Normal struct: resolve all fields.
	for _, rf := range raw.Fields {
		fields := b.resolveRawField(rf)
		if len(fields) > 0 {
			wt.Fields = append(wt.Fields, fields...)
		}
	}
}

// resolveRawField converts a model.RawField into one or more WorkingField entries.
// At this stage, we:
//   - apply exclude-by-tag filters
//   - compute tags (respecting KeepORMTags)
//   - mark Deprecated flag (for later filtering)
//   - attach the resolved WorkingType.
func (b *Builder) resolveRawField(rf *model.RawField) []*model.WorkingField {
	if rf == nil {
		return nil
	}

	// Skip by tag filters.
	if b.shouldOmitFieldByTag(rf) {
		return nil
	}

	// Build tag map from raw literal.
	tagMap := parseStructTagLit(rf.TagLit)
	rawTag := buildTagLiteral(tagMap)

	// Drop orm tags if requested.
	if !b.opts.KeepORMTags {
		delete(tagMap, "gorm")
		delete(tagMap, "db")
	}
	tag := buildTagLiteral(tagMap)

	t := b.resolveTypeExpr(rf.TypeExpr)

	deprecated := false
	if b.opts.ExcludeDeprecated && (strings.Contains(rf.Comment, "Deprecated") || strings.Contains(rf.Comment, "deprecated")) {
		deprecated = true
	}

	wf := &model.WorkingField{
		RawName:    rf.Name,
		Name:       rf.Name,
		Comment:    rf.Comment,
		Embedded:   rf.IsEmbedded,
		Type:       t,
		Tag:        reflect.StructTag(strings.Trim(tag, "`")),
		RawTag:     reflect.StructTag(strings.Trim(rawTag, "`")),
		Omit:       false,
		Deprecated: deprecated,
	}

	return []*model.WorkingField{wf}
}

// resolveTypeExpr resolves an ast.Expr into a WorkingType graph.
func (b *Builder) resolveTypeExpr(expr ast.Expr) *model.WorkingType {
	switch t := expr.(type) {
	case *ast.Ident:
		return b.resolveIdentType(t)

	case *ast.StarExpr:
		elem := b.resolveTypeExpr(t.X)
		return &model.WorkingType{
			Kind:       model.KindPointer,
			Underlying: elem,
		}

	case *ast.ArrayType:
		elem := b.resolveTypeExpr(t.Elt)
		return &model.WorkingType{
			Kind:       model.KindSlice,
			Underlying: elem,
		}
	case *ast.IndexExpr:
		// Single-type-argument generic T[A]
		// Examples:
		//   AuditModel[uuid.UUID]
		//   MutableModel[*User]
		baseType := b.resolveTypeExpr(t.X)
		if baseType == nil {
			return &model.WorkingType{Name: "UNKNOWN", Kind: model.KindBuiltin}
		}

		// Resolve the single type argument
		argWT := b.resolveTypeExpr(t.Index)

		return b.instantiateGeneric(baseType, []*model.WorkingType{argWT})

	case *ast.IndexListExpr:
		// Multi-type-argument generic T[A,B,...]
		// Example:
		//   Paginated[User, Meta]
		baseType := b.resolveTypeExpr(t.X)
		if baseType == nil {
			return &model.WorkingType{Name: "UNKNOWN", Kind: model.KindBuiltin}
		}

		args := make([]*model.WorkingType, len(t.Indices))
		for i, expr := range t.Indices {
			args[i] = b.resolveTypeExpr(expr)
		}

		return b.instantiateGeneric(baseType, args)
	case *ast.SelectorExpr:
		pkgPath, typeName := b.resolveSelector(t)
		return b.resolveExternalType(pkgPath, typeName)

	default:
		return &model.WorkingType{Name: "UNKNOWN", Kind: model.KindBuiltin}
	}
}

// instantiateGeneric applies type arguments to a generic base WorkingType.
// base must be a WorkingType representing the generic definition.
func (b *Builder) instantiateGeneric(base *model.WorkingType, args []*model.WorkingType) *model.WorkingType {
	if base == nil {
		return &model.WorkingType{Name: "UNKNOWN", Kind: model.KindBuiltin}
	}

	// If base is an alias that wraps a struct, expand first
	if base.Kind == model.KindAlias && base.Underlying != nil {
		base = base.Underlying
	}

	// Ensure local generic base structs have their fields populated
	// before we decide we cannot specialize them. This matters when
	// instantiateGeneric is called while the Builder is still populating
	// other types.
	if !base.IsExternal && base.Kind == model.KindStruct && len(base.Fields) == 0 {
		if rs := b.raws.Find(base.Name); rs != nil {
			b.populateFields(base)
		}
	}

	// If base is external or builtin, we cannot safely param-substitute.
	// Just return the external leaf.
	if base.IsExternal || base.Kind != model.KindStruct || len(base.Fields) == 0 {
		return base
	}

	// At this point `base` is a concrete generic definition with fields and
	// its type parameter names are available via base.TypeParams (from the
	// RawStruct.TypeParams captured in Parser.collectStructs).

	// Clone a new WorkingType instance (deep copy of fields)
	inst := &model.WorkingType{
		Name:       base.Name,
		PkgPath:    base.PkgPath,
		Kind:       model.KindStruct,
		Fields:     make([]*model.WorkingField, 0, len(base.Fields)),
		Comment:    base.Comment,
		IsExternal: base.IsExternal,
	}

	// Use the REAL generic parameter names discovered from the AST (RawStruct→WorkingType)
	paramNames := base.TypeParams
	if len(paramNames) != len(args) {
		// Fallback: mismatch — preserve previous behavior, but do NOT hard-code "T"
		paramNames = make([]string, len(args))
		for i := range args {
			paramNames[i] = fmt.Sprintf("T%d", i)
		}
	}

	// Perform parameter substitution in each field type
	for _, f := range base.Fields {
		newField := *f // shallow copy ok, we'll rewrite Type
		newField.Type = b.substituteParamsInWT(f.Type, paramNames, args)
		inst.Fields = append(inst.Fields, &newField)
	}

	return inst
}

// substituteParamsInWT rewrites a WorkingType by substituting generic parameters.
func (b *Builder) substituteParamsInWT(
	wt *model.WorkingType,
	params []string,
	args []*model.WorkingType,
) *model.WorkingType {

	if wt == nil {
		return nil
	}

	// Leaf type: if wt.Name matches a generic parameter, replace it.
	for i, p := range params {
		if wt.Name == p {
			return args[i]
		}
	}

	// Structural types: recursively rewrite children.
	switch wt.Kind {
	case model.KindPointer:
		return &model.WorkingType{
			Kind:       model.KindPointer,
			Underlying: b.substituteParamsInWT(wt.Underlying, params, args),
		}
	case model.KindSlice:
		return &model.WorkingType{
			Kind:       model.KindSlice,
			Underlying: b.substituteParamsInWT(wt.Underlying, params, args),
		}
	default:
		// Struct or builtin or alias: no structural rewrite needed.
		return wt
	}
}

// resolveTypeExprAlias handles RawStruct alias info (Alias + AliasPtr).
// It produces the underlying WorkingType to which an alias points,
// typically []T or []*T.
func (b *Builder) resolveTypeExprAlias(aliasName string, aliasPtr *bool) *model.WorkingType {
	// First try local type
	elem := b.byName[aliasName]
	if elem == nil {
		// External? Then load its struct definition.
		if raw := b.loadExternalRawStruct("", aliasName); raw != nil {
			// Create a WorkingType shell and attach fields
			elem = b.ensureWorkingType(aliasName)
			for _, rf := range raw.Fields {
				fields := b.resolveRawField(rf)
				if len(fields) > 0 {
					elem.Fields = append(elem.Fields, fields...)
				}
			}
		} else {
			// If still unknown, fallback to builtin-ish
			elem = &model.WorkingType{Name: aliasName, Kind: model.KindStruct}
		}
	}

	// Optional pointer element slice.
	if aliasPtr != nil && *aliasPtr {
		ptr := &model.WorkingType{
			Kind:       model.KindPointer,
			Underlying: elem,
		}
		return &model.WorkingType{
			Kind:       model.KindSlice,
			Underlying: ptr,
		}
	}

	// Non-pointer slice element.
	return &model.WorkingType{
		Kind:       model.KindSlice,
		Underlying: elem,
	}
}

// -----------------------------------------------------------------------------
// Helper: field / tag filters
// -----------------------------------------------------------------------------

// shouldOmitFieldByTag implements Options.ExcludeByTags for a field.
func (b *Builder) shouldOmitFieldByTag(rf *model.RawField) bool {
	if rf == nil || rf.TagLit == nil {
		return false
	}
	if len(b.opts.ExcludeByTags) == 0 {
		return false
	}

	raw := strings.Trim(rf.TagLit.Value, "`")
	if raw == "" {
		return false
	}

	st := reflect.StructTag(raw)
	for _, f := range b.opts.ExcludeByTags {
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

// -----------------------------------------------------------------------------
// Helper: identifier / selector resolution
// -----------------------------------------------------------------------------

var builtinIdents = map[string]struct{}{
	"string": {}, "bool": {}, "byte": {}, "rune": {}, "int": {}, "int8": {}, "int16": {},
	"int32": {}, "int64": {}, "uint": {}, "uint8": {}, "uint16": {}, "uint32": {}, "uint64": {},
	"float32": {}, "float64": {}, "complex64": {}, "complex128": {}, "error": {},
}

// resolveIdentType handles plain identifiers – builtins vs local structs.
func (b *Builder) resolveIdentType(id *ast.Ident) *model.WorkingType {
	if id == nil {
		return &model.WorkingType{Name: "UNKNOWN", Kind: model.KindBuiltin}
	}

	name := id.Name

	// Primitive/builtin?
	if _, ok := builtinIdents[name]; ok {
		return &model.WorkingType{Name: name, Kind: model.KindBuiltin}
	}

	// Local struct?
	if rs := b.raws.Find(name); rs != nil {
		return b.ensureWorkingType(name)
	}

	// Generic alias?
	if b.parser != nil {
		if ea, ok := b.parser.externalAliases[name]; ok {
			return b.buildExternalAliasType(name, ea)
		}
	}

	// External type only referenced by name (e.g., Time)
	for _, meta := range b.imports {
		if _, st, err := b.parser.getExternalStructAST(meta.Path, name); err == nil && st != nil {
			return b.resolveExternalType(meta.Path, name)
		}
	}

	// Unknown → fallback
	return &model.WorkingType{Name: name, Kind: model.KindBuiltin}
}

func (b *Builder) buildExternalAliasType(aliasName string, ea ExternalAlias) *model.WorkingType {
	wt := &model.WorkingType{
		Name:       aliasName,
		PkgPath:    ea.PkgPath,
		Kind:       model.KindStruct,
		IsExternal: true,
		Fields:     []*model.WorkingField{},
	}

	if b.parser == nil {
		return wt
	}

	file, st, err := b.parser.getExternalStructAST(ea.PkgPath, ea.TypeName)
	_ = file
	if err != nil || st == nil {
		return wt
	}

	// Get the external struct's fields
	rawFields := b.parser.rawFieldsFromAST(st)

	// Simple, practical specialization:
	// If there is exactly one type argument, assume the type parameter name is "T"
	// in the external definition and substitute T -> that argument in field types.
	if len(ea.TypeArgs) == 1 {
		arg := ea.TypeArgs[0]
		for _, rf := range rawFields {
			rf.TypeExpr = substituteTypeParam(rf.TypeExpr, "T", arg)
		}
	}

	// Convert RawFields -> WorkingFields
	for _, rf := range rawFields {
		fields := b.resolveRawField(rf)
		if len(fields) > 0 {
			wt.Fields = append(wt.Fields, fields...)
		}
	}

	return wt
}

// resolveSelector maps a SelectorExpr (pkg.Type) to (importPath, typeName)
// using the Builder's imports map (alias → ImportMeta).
func (b *Builder) resolveSelector(sel *ast.SelectorExpr) (pkgPath, typeName string) {
	if sel == nil || sel.Sel == nil {
		return "", ""
	}

	typeName = sel.Sel.Name

	pkgIdent, ok := sel.X.(*ast.Ident)
	if !ok || pkgIdent.Name == "" {
		return "", typeName
	}

	alias := pkgIdent.Name

	// 1) local package imports (your own model package)
	if meta, ok := b.imports[alias]; ok {
		return meta.Path, typeName
	}

	// fallback: unresolved
	return "", typeName
}

// resolveExternalType creates an opaque WorkingType representing an external type.
// For now we treat external types as struct-like leaves and do not expand fields.
func (b *Builder) resolveExternalType(pkgPath, typeName string) *model.WorkingType {
	if typeName == "" {
		typeName = "UNKNOWN"
	}

	wt := &model.WorkingType{
		Name:       typeName,
		PkgPath:    pkgPath,
		Kind:       model.KindStruct,
		IsExternal: true,
		Fields:     []*model.WorkingField{},
	}

	if b.parser != nil {
		if raw := b.loadExternalRawStruct(pkgPath, typeName); raw != nil {
			for _, rf := range raw.Fields {
				fields := b.resolveRawField(rf)
				if len(fields) > 0 {
					wt.Fields = append(wt.Fields, fields...)
				}
			}
		}
	}

	return wt
}

// loadExternalRawStruct loads an external struct's RawStruct definition
// using parser.getExternalStructAST + parser.rawFieldsFromAST.
// Returns nil if the type does not exist or is not a struct.
func (b *Builder) loadExternalRawStruct(pkgPath, typeName string) *model.RawStruct {
	if pkgPath == "" || typeName == "" || b.parser == nil {
		return nil
	}

	file, st, err := b.parser.getExternalStructAST(pkgPath, typeName)
	if err != nil || st == nil {
		return nil
	}

	fields := b.parser.rawFieldsFromExternalAST(pkgPath, file, st)

	return &model.RawStruct{
		Name:    typeName,
		PkgPath: pkgPath,
		Fields:  fields,
		File:    file,
	}
}

// -----------------------------------------------------------------------------
// Tag helpers (copied/adapted from old parser logic)
// -----------------------------------------------------------------------------

// parseStructTagLit parses an ast.BasicLit struct tag literal into a map.
func parseStructTagLit(lit *ast.BasicLit) map[string]string {
	m := map[string]string{}
	if lit == nil {
		return m
	}
	tag := strings.Trim(lit.Value, "`")
	for tag != "" {
		parts := strings.SplitN(tag, ":\"", 2)
		if len(parts) != 2 {
			break
		}
		key := parts[0]
		rest := parts[1]
		end := strings.Index(rest, "\"")
		if end < 0 {
			break
		}
		val := rest[:end]
		m[key] = val
		tag = strings.TrimSpace(rest[end+1:])
	}
	return m
}

// -----------------------------------------------------------------------------
// Embedded / inline handling
// -----------------------------------------------------------------------------

// isTagEmbedded checks well-known embedded/inline indicators on a tag.
func (b *Builder) isTagEmbedded(tag reflect.StructTag) bool {
	if tag == "" {
		return false
	}

	// Mirror tagEmbedded behaviour.
	embeddedTags := []TagFilter{
		{Key: "gorm", Value: "embedded"},
		{Key: "db", Value: "embedded"},
		{Key: "json", Value: "inline"},
		{Key: "yaml", Value: "inline"},
		{Key: "mapstructure", Value: "squash"},
	}

	for _, f := range embeddedTags {
		if v, ok := tag.Lookup(f.Key); ok {
			for _, part := range strings.Split(v, ";") {
				if part == f.Value {
					return true
				}
			}
		}
	}

	return false
}

/*
You have this information already. You must base your patches cumulatively on the content provided to you. Please start from the files attached to the project (i keep these up to date as much as possible). Then, apply any changes you suggest that I approve to your working knowledge of these exact files. Then, generate the patch. Then, apply that patch to your working knowledge of these files unless I say otherwise.

Also, if you feel like you're missing
*/
// flattenEmbedded flattens anonymous embedded fields when FlattenEmbedded is true.
// It does NOT handle tag-based embedding; see flattenTagEmbedded.
func (b *Builder) flattenEmbedded(wt *model.WorkingType) {
	if wt == nil || !b.opts.FlattenEmbedded {
		return
	}
	if wt.Kind != model.KindStruct {
		return
	}

	out := make([]*model.WorkingField, 0, len(wt.Fields))
	for _, f := range wt.Fields {
		if f == nil {
			continue
		}
		if f.Embedded {
			// If FlattenEmbedded, REMOVE the wrapper regardless of struct-ness.
			if b.opts.FlattenEmbedded {
				if f.Type != nil && f.Type.Kind == model.KindStruct && len(f.Type.Fields) > 0 {
					// inline real fields
					out = append(out, f.Type.Fields...)
				}
				// either way: DROP the wrapper
				continue
			}

			// if IncludeEmbedded: keep wrapper + inline if possible
			if b.opts.IncludeEmbedded {
				out = append(out, f)
				if f.Type != nil && f.Type.Kind == model.KindStruct && len(f.Type.Fields) > 0 {
					out = append(out, f.Type.Fields...)
				}
				continue
			}
		}
		out = append(out, f)
	}
	wt.Fields = out
}

// flattenTagEmbedded inlines fields based on tag markers.
// Behaviour depends on options:
//   - FlattenEmbedded: remove the wrapper field, inline only inner fields.
//   - IncludeEmbedded: keep wrapper field AND append inner fields.
func (b *Builder) flattenTagEmbedded(wt *model.WorkingType) {
	if wt == nil || wt.Kind != model.KindStruct {
		return
	}

	out := make([]*model.WorkingField, 0, len(wt.Fields))
	for _, f := range wt.Fields {
		if f == nil {
			continue
		}
		inline := b.isTagEmbedded(f.RawTag)
		if !inline || f.Type == nil || f.Type.Kind != model.KindStruct {
			out = append(out, f)
			continue
		}

		switch {
		case b.opts.FlattenEmbedded:
			// Replace wrapper with its fields.
			out = append(out, f.Type.Fields...)
		case b.opts.IncludeEmbedded:
			// Keep wrapper and also inline inner fields.
			out = append(out, f)
			out = append(out, f.Type.Fields...)
		default:
			// Neither flatten nor include embedded: keep wrapper only.
			out = append(out, f)
		}
	}
	wt.Fields = out
}

// -----------------------------------------------------------------------------
// Transformations driver
// -----------------------------------------------------------------------------

// applyTransformations runs all WorkingType-level transformations in order.
func (b *Builder) applyTransformations(wt *model.WorkingType) {
	if wt == nil || wt.Omit {
		return
	}

	// Exclude whole type by name (Options.ExcludeTypes).
	if b.isTypeExcluded(wt.Name) {
		wt.Omit = true
		return
	}

	b.filterDeprecated(wt)
	if wt.Omit {
		return
	}

	// Flatten embedded fields.
	b.flattenEmbedded(wt)
	b.flattenTagEmbedded(wt)

	// Alias expansion / other alias behaviours can be added here if needed.
	// b.expandAlias(wt) // currently a no-op; left for future use.

	// Apply suffix to type names.
	b.applySuffix(wt)

	// Deduplicate fields.
	b.dedupeFields(wt)
}

// isTypeExcluded checks Options.ExcludeTypes (stored as lowercase) against the name.
func (b *Builder) isTypeExcluded(name string) bool {
	if len(b.opts.ExcludeTypes) == 0 || name == "" {
		return false
	}
	lower := strings.ToLower(name)
	for _, t := range b.opts.ExcludeTypes {
		if t == lower {
			return true
		}
	}
	return false
}

// filterDeprecated applies ExcludeDeprecated at the type and field level.
func (b *Builder) filterDeprecated(wt *model.WorkingType) {
	if wt == nil || !b.opts.ExcludeDeprecated {
		return
	}

	if strings.Contains(wt.Comment, "Deprecated") || strings.Contains(wt.Comment, "deprecated") {
		wt.Omit = true
		return
	}

	out := make([]*model.WorkingField, 0, len(wt.Fields))
	for _, f := range wt.Fields {
		if f == nil || f.Deprecated {
			continue
		}
		out = append(out, f)
	}
	wt.Fields = out
}

// applySuffix appends the configured suffix to the type name if not already present.
func (b *Builder) applySuffix(wt *model.WorkingType) {
	if wt == nil || wt.NameResolved {
		return
	}
	if b.opts.Suffix == "" {
		return
	}
	if strings.HasSuffix(wt.Name, b.opts.Suffix) {
		wt.NameResolved = true
		return
	}
	wt.Name = wt.Name + b.opts.Suffix
	wt.NameResolved = true
}

// dedupeFields removes duplicate field names, keeping the first occurrence.
func (b *Builder) dedupeFields(wt *model.WorkingType) {
	if wt == nil || wt.Kind != model.KindStruct {
		return
	}
	seen := make(map[string]bool, len(wt.Fields))
	out := make([]*model.WorkingField, 0, len(wt.Fields))
	for _, f := range wt.Fields {
		if f == nil {
			continue
		}
		name := f.Name
		if name == "" {
			// Preserve unnamed fields as-is.
			out = append(out, f)
			continue
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, f)
	}
	wt.Fields = out
}

func (b *Builder) expandAlias(wt *model.WorkingType) {
	if wt.Kind != model.KindAlias || wt.AliasApplied {
		return
	}
	wt.AliasApplied = true
	wt.Kind = wt.Underlying.Kind
	wt.Fields = wt.Underlying.Fields
	wt.Underlying = wt.Underlying.Underlying
}

func (b *Builder) applyPluralization(wt *model.WorkingType) {
	if !b.opts.Pluralize {
		return
	}

	for _, f := range wt.Fields {
		if f.Type.Kind == model.KindStruct {
			plural := inflection.Plural(f.Type.Name)
			target := b.byName[plural]
			if target != nil {
				if b.opts.PointerSlice {
					f.Type = &model.WorkingType{
						Kind:       model.KindSlice,
						Underlying: &model.WorkingType{Kind: model.KindPointer, Underlying: target},
					}
				} else {
					f.Type = &model.WorkingType{
						Kind:       model.KindSlice,
						Underlying: target,
					}
				}
			}
		}
	}
}

func substituteTypeParam(expr ast.Expr, paramName string, arg ast.Expr) ast.Expr {
	switch t := expr.(type) {
	case *ast.Ident:
		if t.Name == paramName {
			return arg
		}
		return expr

	case *ast.StarExpr:
		t.X = substituteTypeParam(t.X, paramName, arg)
		return expr

	case *ast.ArrayType:
		t.Elt = substituteTypeParam(t.Elt, paramName, arg)
		return expr

	case *ast.MapType:
		t.Key = substituteTypeParam(t.Key, paramName, arg)
		t.Value = substituteTypeParam(t.Value, paramName, arg)
		return expr

	case *ast.SelectorExpr:
		t.X = substituteTypeParam(t.X, paramName, arg)
		return expr

	case *ast.IndexExpr:
		t.X = substituteTypeParam(t.X, paramName, arg)
		t.Index = substituteTypeParam(t.Index, paramName, arg)
		return expr

	case *ast.IndexListExpr:
		t.X = substituteTypeParam(t.X, paramName, arg)
		for i, e := range t.Indices {
			t.Indices[i] = substituteTypeParam(e, paramName, arg)
		}
		return expr

	default:
		return expr
	}
}
