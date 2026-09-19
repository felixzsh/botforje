package config

import (
	"reflect"
	"regexp"
	"strings"
)

var envRefPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// expandValue resolves ${ENV_VAR} in every string of v and returns the names
// of variables that were referenced but unset (ADR 0014). Expansion runs on
// decoded values, so a secret cannot break the YAML.
func expandValue(v reflect.Value, lookup func(string) (string, bool)) []string {
	seen := map[string]bool{}
	var missing []string
	add := func(name string) {
		if !seen[name] {
			seen[name] = true
			missing = append(missing, name)
		}
	}
	walk(v, lookup, add)
	return missing
}

func walk(v reflect.Value, lookup func(string) (string, bool), missing func(string)) {
	switch v.Kind() {
	case reflect.Pointer:
		if !v.IsNil() {
			walk(v.Elem(), lookup, missing)
		}
	case reflect.Struct:
		t := v.Type()
		for i := 0; i < v.NumField(); i++ {
			if !t.Field(i).IsExported() {
				continue
			}
			walk(v.Field(i), lookup, missing)
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			walk(v.Index(i), lookup, missing)
		}
	case reflect.Map:
		if v.IsNil() {
			return
		}
		for _, k := range v.MapKeys() {
			val := v.MapIndex(k)
			if val.Kind() == reflect.Interface {
				continue // declared context literals are not interpolated
			}
			item := reflect.New(val.Type()).Elem()
			item.Set(val)
			walk(item, lookup, missing)
			v.SetMapIndex(k, item)
		}
	case reflect.String:
		if !v.CanSet() {
			return
		}
		expanded, changed := expandString(v.String(), lookup, missing)
		if changed {
			v.SetString(expanded)
		}
	}
}

func expandString(
	s string,
	lookup func(string) (string, bool),
	missing func(string),
) (string, bool) {
	if !strings.Contains(s, "${") {
		return s, false
	}
	changed := false
	out := envRefPattern.ReplaceAllStringFunc(s, func(ref string) string {
		name := ref[2 : len(ref)-1]
		value, ok := lookup(name)
		if !ok {
			missing(name)
			return ref
		}
		changed = true
		return value
	})
	return out, changed
}
