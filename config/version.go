package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"

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

// writtenValue returns v with every secret replaced by its written form. The
// result is hashed and discarded inside ScopeVersion, so a secret value is
// never carried into a response, a log, or storage.
func writtenValue(v reflect.Value) any {
	switch v.Kind() {
	case reflect.Struct:
		if v.Type() == secretType {
			return writtenSecret(v.Interface().(spec.Secret).Raw())
		}
		fields := make(map[string]any, v.NumField())
		for i := range v.NumField() {
			field := v.Type().Field(i)
			if !field.IsExported() {
				continue
			}
			fields[field.Name] = writtenValue(v.Field(i))
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
		items := make([]any, v.Len())
		for i := range v.Len() {
			items[i] = writtenValue(v.Index(i))
		}
		return items
	default:
		return v.Interface()
	}
}
