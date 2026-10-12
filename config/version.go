package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"strings"

	"github.com/felixzsh/botflota/spec"
)

// ScopeVersion is the opaque logical version of one saved scope: an API editor
// sends it back so a write cannot silently overwrite a newer edit. It is a
// digest of the scope's saved content, not a schema version, a file timestamp,
// or an archive, so equal content always yields the same value and nothing has
// to be stored to keep it (ADR 0016: keys are identities).
func ScopeVersion(scope spec.Scope) (string, error) {
	encoded, err := json.Marshal(writtenValue(reflect.ValueOf(scope)))
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return "v-" + hex.EncodeToString(sum[:8]), nil
}

// writtenSecret marshals a secret as the document writes it — the reference
// when the config used one, otherwise the literal — so two scopes that differ
// only in a secret do not share a version and a stale write still conflicts.
type writtenSecret string

func (s writtenSecret) MarshalJSON() ([]byte, error) { return json.Marshal(string(s)) }

// textType marshals itself: a single variant writes as a scalar and several as
// a list, so it is left alone rather than rebuilt as a plain slice.
var textType = reflect.TypeOf(spec.Text{})

// channelOptionsType keeps the adapter-owned options node untouched, so a
// publication writes back exactly what its author declared (ADR 0054).
var channelOptionsType = reflect.TypeOf(spec.ChannelOptions{})

// yamlField reads a struct field's YAML name, so rebuilt documents keep the
// keys the config actually uses. It reports omitempty, which a writer must honor
// to avoid writing empty settings, inline for a field whose map entries belong
// to its parent, and skip for a field the format drops.
func yamlField(field reflect.StructField) (name string, omitEmpty, inline, skip bool) {
	tag, ok := field.Tag.Lookup("yaml")
	if !ok {
		return field.Name, false, false, false
	}
	parts := strings.Split(tag, ",")
	if parts[0] == "-" {
		return "", false, false, true
	}
	name = parts[0]
	if name == "" {
		name = field.Name
	}
	for _, option := range parts[1:] {
		switch option {
		case "omitempty":
			omitEmpty = true
		case "inline":
			inline = true
		}
	}
	return name, omitEmpty, inline, false
}

// isEmpty reports whether a field is dropped from the document. A nil
// collection is always dropped: writing an empty map where the document had none
// would make a saved scope differ from the loaded one, and every reload would
// then see the scope as changed. An empty one is dropped only where the field
// allows it.
func isEmpty(v reflect.Value, omitEmpty bool) bool {
	switch v.Kind() {
	case reflect.Map, reflect.Slice:
		return v.IsNil() || (omitEmpty && v.Len() == 0)
	case reflect.String:
		return v.Len() == 0
	default:
		return v.IsZero()
	}
}

// writtenValue returns v with every secret replaced by its written form. Scope
// digests the result and a writer encodes it, so a saved credential is never
// replaced by its mask, and no value leaves either function.
func writtenValue(v reflect.Value) any {
	switch v.Kind() {
	case reflect.Struct:
		if v.Type() == secretType {
			return writtenSecret(v.Interface().(spec.Secret).Raw())
		}
		if v.Type() == channelOptionsType {
			return v.Interface()
		}
		fields := make(map[string]any, v.NumField())
		for i := range v.NumField() {
			field := v.Type().Field(i)
			if !field.IsExported() {
				continue
			}
			name, omitEmpty, inline, skip := yamlField(field)
			if skip {
				continue
			}
			value := v.Field(i)
			if isEmpty(value, omitEmpty) {
				continue
			}
			rebuilt := writtenValue(value)
			if inline {
				for key, entry := range rebuilt.(map[string]any) {
					fields[key] = entry
				}
				continue
			}
			fields[name] = rebuilt
		}
		return fields
	case reflect.Pointer:
		if v.IsNil() {
			return nil
		}
		return writtenValue(v.Elem())
	case reflect.Map:
		entries := make(map[string]any, v.Len())
		for _, key := range v.MapKeys() {
			entries[key.String()] = writtenValue(v.MapIndex(key))
		}
		return entries
	case reflect.Slice, reflect.Array:
		if v.Type() == textType {
			return v.Interface()
		}
		items := make([]any, v.Len())
		for i := range v.Len() {
			items[i] = writtenValue(v.Index(i))
		}
		return items
	default:
		return v.Interface()
	}
}
