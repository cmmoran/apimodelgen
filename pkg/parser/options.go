package parser

import (
	"path/filepath"
	"strings"
)

// ImportMeta describes an import needed by generated code.
type ImportMeta struct {
	Path  string // fully‑qualified path
	Name  string // package base name
	Alias string // unique alias chosen for this file
	Dir   string
	Mod   bool
}

// TagFilter excludes a field/type when the struct tag matches Key and contains Value.
type TagFilter struct {
	Key   string
	Value string
}

// Options control parsing and post‑processing.
//
// InDir             – directory to parse
// OutDir            – output directory
// OutFile           – output filename
// Suffix            – append to every struct name.
// PatchSuffix       – append to every struct name for patch files, includes Suffix.
// KeepORMTags       – keep orm-specific tags in generated types, gorm:"..." db:"..." etc
// FlattenEmbedded   – lift anonymous / tag‑inline fields into parent (default true).
// IncludeEmbedded   – keep embedded field itself + inner fields.
// ExcludeDeprecated – skip structs whose leading comment contains "deprecated".
// ExcludeTypes      – names of structs to skip (case‑insensitive).
// ExcludeByTags     – filters to skip fields / referenced types.
// Note: FlattenEmbedded and IncludeEmbedded are mutually exclusive; last one wins.
type Options struct {
	InDir             string      `json:"in_dir,omitempty" yaml:"in_dir,omitempty" toml:"in_dir,omitempty" mapstructure:"in_dir,omitempty"`
	OutDir            string      `json:"out_dir,omitempty" yaml:"out_dir,omitempty" toml:"out_dir,omitempty" mapstructure:"out_dir,omitempty"`
	OutFile           string      `json:"out_file,omitempty" yaml:"out_file,omitempty" toml:"out_file,omitempty" mapstructure:"out_file,omitempty"`
	Suffix            string      `json:"suffix,omitempty" yaml:"suffix,omitempty" toml:"suffix,omitempty" mapstructure:"suffix,omitempty"`
	PatchSuffix       string      `json:"patch_suffix,omitempty" yaml:"patch_suffix,omitempty" toml:"patch_suffix,omitempty" mapstructure:"patch_suffix,omitempty"`
	KeepORMTags       bool        `json:"keep_orm_tags,omitempty" yaml:"keep_orm_tags,omitempty" toml:"keep_orm_tags,omitempty" mapstructure:"keep_orm_tags,omitempty"`
	FlattenEmbedded   bool        `json:"flatten_embedded,omitempty" yaml:"flatten_embedded,omitempty" toml:"flatten_embedded,omitempty" mapstructure:"flatten_embedded,omitempty"`
	IncludeEmbedded   bool        `json:"include_embedded,omitempty" yaml:"include_embedded,omitempty" toml:"include_embedded,omitempty" mapstructure:"include_embedded,omitempty"`
	ExcludeDeprecated bool        `json:"exclude_deprecated,omitempty" yaml:"exclude_deprecated,omitempty" toml:"exclude_deprecated,omitempty" mapstructure:"exclude_deprecated,omitempty"`
	ExcludeTypes      []string    `json:"exclude_types,omitempty" yaml:"exclude_types,omitempty" toml:"exclude_types,omitempty" mapstructure:"exclude_types,omitempty"`
	ExcludeByTags     []TagFilter `json:"exclude_by_tags,omitempty" yaml:"exclude_by_tags,omitempty" toml:"exclude_by_tags,omitempty" mapstructure:"exclude_by_tags,omitempty"`
}

func NewOptions() *Options {
	return &Options{
		InDir:           ".",
		OutDir:          "api",
		OutFile:         "api_gen.go",
		Suffix:          "",
		PatchSuffix:     "Patch",
		KeepORMTags:     false,
		FlattenEmbedded: false,
		IncludeEmbedded: true,
	}
}

func (o *Options) Normalize(excludeByTagsStrings ...string) {
	for _, s := range excludeByTagsStrings {
		sp := strings.Split(s, ":")
		o.ExcludeByTags = append(o.ExcludeByTags, TagFilter{Key: sp[0], Value: sp[1]})
	}
	if o.FlattenEmbedded == o.IncludeEmbedded {
		panic("FlattenEmbedded and IncludeEmbedded are mutually exclusive")
	}
	if strings.Contains(o.InDir, ".") {
		o.InDir, _ = filepath.Abs(o.InDir)
	}
	if len(o.OutDir) == 0 {
		o.OutDir = "dto"
	}
	if strings.Contains(o.OutDir, ".") {
		o.OutDir, _ = filepath.Abs(o.OutDir)
	}
	if len(o.OutFile) == 0 {
		o.OutFile = "api_gen.go"
	}

	// Ensure PatchSuffix always has *some* value
	if o.PatchSuffix == "" {
		o.PatchSuffix = "Patch"
	}
}

// functional option pattern ---------------------------------------------------

type Option func(*Options)

func WithInDir(d string) Option       { return func(o *Options) { o.InDir = d } }
func WithOutDir(d string) Option      { return func(o *Options) { o.OutDir = d } }
func WithOutFile(f string) Option     { return func(o *Options) { o.OutFile = f } }
func WithSuffix(s string) Option      { return func(o *Options) { o.Suffix = s } }
func WithPatchSuffix(s string) Option { return func(o *Options) { o.PatchSuffix = s } }
func WithFlattenEmbedded() Option {
	return func(o *Options) { o.FlattenEmbedded, o.IncludeEmbedded = true, false }
}
func WithIncludeEmbedded() Option {
	return func(o *Options) { o.IncludeEmbedded, o.FlattenEmbedded = true, false }
}
func WithExcludeDeprecated() Option { return func(o *Options) { o.ExcludeDeprecated = true } }
func WithExcludeTypes(names ...string) Option {
	return func(o *Options) {
		for _, n := range names {
			o.ExcludeTypes = append(o.ExcludeTypes, strings.TrimSpace(n))
		}
	}
}
func WithExcludeByTag(key, val string) Option {
	return func(o *Options) { o.ExcludeByTags = append(o.ExcludeByTags, TagFilter{key, val}) }
}
func WithKeepORMTags() Option { return func(o *Options) { o.KeepORMTags = true } }
