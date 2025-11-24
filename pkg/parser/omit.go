package parser

import (
	"reflect"
	"strings"

	"github.com/cmmoran/apimodelgen/pkg/model"
)

// shouldOmitWorkingField determines whether a WorkingField should be omitted
// during API generation based on configured tag filters or explicit dash tags.
func shouldOmitWorkingField(wf *model.WorkingField, opts *Options) bool {
	if wf == nil {
		return false
	}

	tagMap := structTagToMap(wf.RawTag)
	if len(tagMap) == 0 {
		return false
	}

	// When no filters are provided, treat dash-tagged fields as omitted.
	if len(opts.ExcludeByTags) == 0 {
		for _, v := range tagMap {
			if containsTagPart(v, "-") {
				return true
			}
		}
		return false
	}

	for _, f := range opts.ExcludeByTags {
		v, ok := tagMap[f.Key]
		if !ok {
			continue
		}
		if containsTagPart(v, f.Value) {
			return true
		}
	}

	return false
}

// structTagToMap converts a reflect.StructTag into a key/value map.
func structTagToMap(tag reflect.StructTag) map[string]string {
	m := map[string]string{}
	if tag == "" {
		return m
	}

	raw := string(tag)
	for raw != "" {
		parts := strings.SplitN(raw, ":\"", 2)
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

		raw = strings.TrimSpace(rest[end+1:])
	}

	return m
}

// containsTagPart splits a tag value on common delimiters and reports whether
// any fragment matches the expected value.
func containsTagPart(tagVal, expected string) bool {
	if tagVal == "" {
		return false
	}

	for _, part := range strings.FieldsFunc(tagVal, func(r rune) bool {
		return r == ';' || r == ','
	}) {
		if part == expected {
			return true
		}
	}

	return false
}
