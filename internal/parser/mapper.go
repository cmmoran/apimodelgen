package parser

import (
	"reflect"
	"strings"
	"unicode"

	"github.com/cmmoran/apimodelgen/internal/model"
)

// ToApiStructs converts a set of WorkingTypes into ApiStructs ready for rendering.
func ToApiStructs(types []*model.WorkingType, opts *Options) []*model.ApiStruct {
	out := make([]*model.ApiStruct, 0, len(types))

	for _, wt := range types {
		if wt == nil || wt.Omit {
			continue
		}

		// ------------------------------------------------------------
		// APPLY TYPE-LEVEL EXCLUSIONS
		// ------------------------------------------------------------
		if len(opts.ExcludeTypes) > 0 {
			// Skip generic template types entirely; they serve as blueprints
			// for concrete instantiations but should not be emitted as DTOs.
			if len(wt.TypeParams) > 0 {
				continue
			}
			name := wt.Name

			// Strip DTO suffix if present (so user can specify base type)
			if opts.Suffix != "" && strings.HasSuffix(name, opts.Suffix) {
				name = strings.TrimSuffix(name, opts.Suffix)
			}

			skip := false
			for _, ex := range opts.ExcludeTypes {
				if strings.EqualFold(ex, name) {
					skip = true
					break
				}
			}
			if skip {
				continue
			}
		}

		// ------------------------------------------------------------
		// SKIP ALIAS TYPES WHOSE UNDERLYING TARGET TYPE IS EXCLUDED
		// ------------------------------------------------------------
		if wt.Kind == model.KindAlias && wt.Underlying != nil {
			baseName := wt.Underlying.Name

			// Strip suffix so user-specified type matches
			if opts.Suffix != "" && strings.HasSuffix(baseName, opts.Suffix) {
				baseName = strings.TrimSuffix(baseName, opts.Suffix)
			}

			for _, ex := range opts.ExcludeTypes {
				if strings.EqualFold(ex, baseName) {
					// do NOT emit an ApiStruct for this alias
					goto skipEmit
				}
			}
		}

		// ------------------------------------------------------------
		// EMIT STRUCT OR ALIAS
		// ------------------------------------------------------------
		switch wt.Kind {
		case model.KindStruct:
			if as := workingStructToApiStruct(wt, opts); as != nil {
				out = append(out, as)
			}

		case model.KindAlias:
			if as := workingAliasToApiStruct(wt, opts); as != nil {
				out = append(out, as)
			}
		}

		continue

	skipEmit:
		continue
	}

	return out
}

// -----------------------------------------------------------------------------
// Struct mapping
// -----------------------------------------------------------------------------

func workingStructToApiStruct(wt *model.WorkingType, opts *Options) *model.ApiStruct {
	api := &model.ApiStruct{
		Name:     wt.Name,
		Alias:    nil,
		AliasPtr: nil,
		Comment:  wt.Comment,
		Fields:   make([]*model.ApiField, 0, len(wt.Fields)),
		Imports:  make(map[string]bool),
		PkgName:  "",
	}

	for _, wf := range wt.Fields {
		if wf == nil || wf.Omit {
			continue
		}
		// Allow anonymous embedded fields when IncludeEmbedded is active.
		if wf.Name == "" && wf.Embedded && opts.IncludeEmbedded {
			// allow it
		} else if !isExportedName(wf.Name) {
			continue
		}

		tf := workingFieldToApiField(wf)
		api.Fields = append(api.Fields, tf)

		// Track imports based on leaf type package path.
		trackImportsFromTypeRef(api.Imports, tf.Type)
	}

	return api
}

func workingFieldToApiField(wf *model.WorkingField) *model.ApiField {
	af := &model.ApiField{
		Name:       wf.Name,
		Type:       workingTypeToTypeRef(wf.Type),
		Tag:        wf.Tag,
		RawTag:     wf.RawTag,
		Comment:    wf.Comment,
		Omit:       wf.Omit,
		IsEmbedded: wf.Embedded,
	}
	if wf.Embedded {
		af.Name = wf.Type.Name // type name becomes field selector name
	} else {
		af.Name = wf.Name
	}

	return af
}

// -----------------------------------------------------------------------------
// Alias mapping (pluralized alias types etc.)
// -----------------------------------------------------------------------------

// workingAliasToApiStruct maps alias WorkingTypes into ApiStruct alias
// declarations understood by GenerateApiFile.
//
// Today we focus on the pattern:
//
//	type Users []User
//	type Users []*User
//
// represented as:
//
//	KindAlias
//	  Underlying: KindSlice
//	    Underlying: KindStruct (or KindAlias already flattened)
func workingAliasToApiStruct(wt *model.WorkingType, opts *Options) *model.ApiStruct {
	if wt.Underlying == nil {
		return nil
	}

	// We only emit alias ApiStructs when the alias is a slice type.
	if wt.Underlying.Kind != model.KindSlice || wt.Underlying.Underlying == nil {
		return nil
	}

	elem := wt.Underlying.Underlying
	isPtr := elem.Kind == model.KindPointer && elem.Underlying != nil

	// Determine the singular type name (alias target) from element.
	target := elem
	if isPtr {
		target = elem.Underlying
	}
	if target == nil {
		return nil
	}

	aliasName := target.Name
	aliasPtr := isPtr

	return &model.ApiStruct{
		Name:     wt.Name,
		Alias:    &aliasName,
		AliasPtr: &aliasPtr,
		Comment:  wt.Comment,
		Fields:   []*model.ApiField{}, // no fields for alias
		Imports:  make(map[string]bool),
		PkgName:  "",
	}
}

// -----------------------------------------------------------------------------
// WorkingType → TypeRef mapping
// -----------------------------------------------------------------------------

// workingTypeToTypeRef converts a WorkingType graph into the existing
// model.TypeRef structure, which GenerateApiFile uses to emit jen code.
func workingTypeToTypeRef(wt *model.WorkingType) *model.TypeRef {
	if wt == nil {
		return &model.TypeRef{Name: "UNKNOWN"}
	}

	switch wt.Kind {

	case model.KindPointer:
		inner := workingTypeToTypeRef(wt.Underlying)
		// Ensure the inner node is not itself marked as pointer; we represent
		// pointer-ness at this level.
		inner.IsPtr = false
		return &model.TypeRef{
			IsPtr: true,
			Elem:  inner,
		}

	case model.KindSlice:
		inner := workingTypeToTypeRef(wt.Underlying)
		return &model.TypeRef{
			IsSlice: true,
			Elem:    inner,
		}

	case model.KindStruct, model.KindBuiltin, model.KindAlias:
		// Leaf type – imported or local.
		return &model.TypeRef{
			PkgPath: wt.PkgPath,
			Name:    wt.Name,
		}

	default:
		return &model.TypeRef{Name: "UNKNOWN"}
	}
}

// -----------------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------------

func isExportedName(name string) bool {
	if name == "" {
		return false
	}
	r := []rune(name)[0]
	return unicode.IsUpper(r)
}

// trackImportsFromTypeRef gathers package paths referenced by TypeRef into the
// given imports set. This complements Parser.collectImportsForTypeRef, which
// also populates ApiImports.
func trackImportsFromTypeRef(imports map[string]bool, tr *model.TypeRef) {
	if tr == nil {
		return
	}
	if tr.PkgPath != "" {
		imports[tr.PkgPath] = true
	}
	if tr.Elem != nil {
		trackImportsFromTypeRef(imports, tr.Elem)
	}
}

// CloneTag is an example helper if you ever need to deep-copy tags later.
// Not used right now but left here for clarity.
func cloneTag(t reflect.StructTag) reflect.StructTag {
	if t == "" {
		return ""
	}
	return reflect.StructTag(string(t))
}
