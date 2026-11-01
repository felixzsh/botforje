package config

import (
	"fmt"
	"reflect"
	"strings"
)

// checkSecretRefs reports every `${...}` reference an entity declares outside
// its secret fields (ADR 0014); the site names the field by its YAML name.
func checkSecretRefs(cfg *Config, add addError, kind, scope, name string, v any) {
	for _, site := range secretRefSites(reflect.ValueOf(v), "") {
		file := cfg.originOf(kind, scope, name)
		entity := fmt.Sprintf("%s %q (scope %q)", kind, name, scope)
		add(file, entity, "%s: a secret reference is only allowed in a model "+
			"api_key and request header values", site)
	}
}

// checkSecretRefValue reports one non-secret string that carries a `${...}`
// reference (ADR 0014). A reference outside a secret field is never expanded,
// so the only place it could reach is a message, a layout, or a path.
func checkSecretRefValue(add addError, file, entity, site, value string) {
	if !strings.Contains(value, "${") {
		return
	}
	add(file, entity, "%s: a secret reference is only allowed in a model "+
		"api_key and request header values", site)
}

// secretRefSites walks v and returns the site of every ordinary string that
// carries a `${...}` reference; a spec.Secret field ends the walk.
func secretRefSites(v reflect.Value, path string) []string {
	var sites []string
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if !v.IsNil() {
			sites = append(sites, secretRefSites(v.Elem(), path)...)
		}
	case reflect.Struct:
		if v.Type() == secretType {
			return nil
		}
		t := v.Type()
		for i := 0; i < v.NumField(); i++ {
			field := t.Field(i)
			if !field.IsExported() {
				continue
			}
			name := yamlName(field)
			if name == "" || name == "-" {
				continue
			}
			sites = append(sites, secretRefSites(v.Field(i), joinPath(path, name))...)
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			sites = append(sites, secretRefSites(v.Index(i),
				fmt.Sprintf("%s[%d]", path, i))...)
		}
	case reflect.Map:
		for _, key := range v.MapKeys() {
			sites = append(sites, secretRefSites(v.MapIndex(key),
				fmt.Sprintf("%s[%s]", path, key))...)
		}
	case reflect.String:
		if strings.Contains(v.String(), "${") {
			sites = append(sites, path)
		}
	}
	return sites
}

func joinPath(path, name string) string {
	if path == "" {
		return name
	}
	return path + "." + name
}

func yamlName(field reflect.StructField) string {
	name, _, _ := strings.Cut(field.Tag.Get("yaml"), ",")
	return name
}
