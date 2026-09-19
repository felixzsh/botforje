package config

import (
	"reflect"
	"regexp"
	"strings"

	"github.com/felixzsh/botflota/spec"
)

// envRefPattern matches a secret reference. The name is deliberately loose:
// anything between the braces is reported as an unresolved variable, so a
// malformed reference fails at load instead of being sent literally
// (ADR 0014).
var envRefPattern = regexp.MustCompile(`\$\{([^}]*)\}`)

var secretType = reflect.TypeOf(spec.Secret{})

// expandValue resolves ${ENV_VAR} in every secret field of v and returns the
// names of variables that were referenced but unset (ADR 0014). Only
// spec.Secret fields are expanded: an ordinary string keeps its text, and
// validation rejects a reference in it. Expansion runs on decoded values, so a
// secret cannot break the YAML.
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
		if v.Type() == secretType {
			expandSecret(v, lookup, missing)
			return
		}
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
	}
}

// expandSecret resolves the references of one secret field and keeps the
// string as written, so serialization shows the placeholder and only the
// consumer reveals the value (ADR 0014).
func expandSecret(v reflect.Value, lookup func(string) (string, bool), missing func(string)) {
	if !v.CanSet() {
		return
	}
	secret := v.Interface().(spec.Secret)
	raw := secret.Raw()
	if !strings.Contains(raw, "${") {
		return
	}
	expanded, _ := expandString(raw, lookup, missing)
	v.Set(reflect.ValueOf(spec.NewSecretRef(raw, expanded)))
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
