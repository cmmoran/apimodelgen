package parser

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

var (
	ErrEmptyPath = errors.New("empty import path")
	ErrStdLib    = errors.New("stdlib")
)

// getExternalStructAST returns the *ast.StructType for `typeName` in `importPath`,
// parsing the package dir on first use, and caching the result.
func (p *Parser) getExternalStructAST(importPath, typeName string) (*ast.File, *ast.StructType, error) {
	// init cache map
	if p.extPkgs == nil {
		p.extPkgs = make(map[string]*externalPkg)
	}

	ep, seen := p.extPkgs[importPath]
	if !seen {
		// locate on-disk directory from your importMap / go.mod info
		_, pkgDir, err := p.resolvePkgDir(importPath)
		if err != nil {
			return nil, nil, fmt.Errorf("unknown import %q: %w", importPath, err)
		}

		// parse all Go files in that dir
		fset := token.NewFileSet()
		pkgs, err := parser.ParseDir(fset, pkgDir, nil, parser.ParseComments)
		if err != nil {
			return nil, nil, fmt.Errorf("parsing %s: %w", pkgDir, err)
		}

		files := make(map[string]*ast.File)
		for _, pkg := range pkgs {
			for fname, f := range pkg.Files {
				files[fname] = f
			}
		}

		ep = &externalPkg{
			files:         files,
			typToFile:     make(map[*ast.StructType]*ast.File),
			structs:       make(map[string]*ast.StructType),
			typeAliases:   make(map[string]ast.Expr),
			importAliases: make(map[string]string),
		}

		// Build import alias map and register imports in p.Imports
		for _, file := range files {
			for _, imp := range file.Imports {
				path := strings.Trim(imp.Path.Value, `"`)

				base := filepath.Base(path)
				alias := base
				if imp.Name != nil && imp.Name.Name != "_" && imp.Name.Name != "." {
					alias = imp.Name.Name
				}

				// Per-package alias→path
				if _, ok := ep.importAliases[alias]; !ok {
					ep.importAliases[alias] = path
				}

				// Also make sure Parser knows about this import so that
				// buildTypeRef/typeExprToJen can assign PkgPath and import it.
				if _, ok := p.Imports[alias]; !ok {
					p.Imports[alias] = &ImportMeta{
						Path:  path,
						Name:  base,
						Alias: alias,
						// Mod=false is fine here; this is “normal” import
						Mod: false,
					}
				}
			}
		}

		// Collect type aliases (e.g. type Time = time.Time)
		for _, file := range files {
			for _, decl := range file.Decls {
				gen, ok := decl.(*ast.GenDecl)
				if !ok || gen.Tok != token.TYPE {
					continue
				}
				for _, spec := range gen.Specs {
					ts, ok := spec.(*ast.TypeSpec)
					if !ok {
						continue
					}
					// Only true aliases (type X = Y), not new named types.
					if ts.Assign.IsValid() {
						ep.typeAliases[ts.Name.Name] = ts.Type
					}
				}
			}
		}

		p.extPkgs[importPath] = ep
	}

	// Already cached?
	if st, ok := ep.structs[typeName]; ok {
		return ep.typToFile[st], st, nil
	}

	// Scan for `type <typeName> struct { ... }`
	for _, file := range ep.files {
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.TYPE {
				continue
			}
			for _, spec := range gen.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				if ts.Name.Name != typeName {
					continue
				}
				st, ok := ts.Type.(*ast.StructType)
				if !ok {
					return nil, nil, fmt.Errorf("%s.%s is not a struct", importPath, typeName)
				}
				ep.structs[typeName] = st
				ep.typToFile[st] = file
				return file, st, nil
			}
		}
	}

	return nil, nil, fmt.Errorf("type %s not found in %s", typeName, importPath)
}

// resolvePkgDir takes a full import path like
//
//	"github.com/foo/bar/pkg/database/model"
//
// and returns
//
//	modulePath = "github.com/foo/bar"
//	pkgDir     = "/.../pkg/mod/github.com/foo/bar@v1.2.3/pkg/database/model"
//
// or an error if none of your modules match.
func (p *Parser) resolvePkgDir(importPath string) (modulePath, pkgDir string, err error) {
	// split into path segments
	parts := strings.Split(importPath, "/")

	// try every possible prefix, longest first
	for i := len(parts); i > 0; i-- {
		candidate := strings.Join(parts[:i], "/")

		// scan your Imports map for a meta whose Path == candidate
		if meta, ok := p.findImportMetaByModulePath(candidate); ok {
			// the remaining segments are the sub-package
			sub := strings.Join(parts[i:], "/")
			if sub == "" {
				return candidate, meta.Dir, nil
			}
			// join the on-disk module root with the subfolder
			return candidate, filepath.Join(meta.Dir, filepath.FromSlash(sub)), nil
		}
	}

	return "", "", fmt.Errorf("no module found for import %q", importPath)
}

// findImportMetaByModulePath returns the ImportMeta whose .Path
// matches exactly modulePath.  Since your p.Imports map is keyed
// by both alias AND module path, we look at values.
func (p *Parser) findImportMetaByModulePath(modulePath string) (*ImportMeta, bool) {
	for _, meta := range p.Imports {
		if meta.Path == modulePath && meta.Dir != "" {
			return meta, true
		}
	}
	return nil, false
}
