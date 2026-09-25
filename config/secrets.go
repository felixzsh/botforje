package config

import (
	"reflect"

	"github.com/felixzsh/botflota/spec"
)

// RestoreMaskedSecrets returns submitted with every masked secret replaced by
// the one the files hold. A client reads a scope, sees `***` where a credential
// is, and sends the document back; without this the mask would be saved as the
// credential and the instance would stop authenticating (ADR 0014).
//
// The rules are deliberately small: a mask keeps the saved value, and any other
// string is the new one — a `${ENV_VAR}` reference, a literal, or an empty
// string that removes it. A field the client left out is not resurrected,
// because a publication replaces the whole scope.
func RestoreMaskedSecrets(saved, submitted spec.Scope) spec.Scope {
	restoreValue(reflect.ValueOf(saved), reflect.ValueOf(&submitted).Elem())
	return submitted
}

// restoreValue walks the saved and submitted documents together and rewrites
// the submitted one in place. Only the submitted side decides what a document
// holds, so an entity the client dropped stays dropped.
func restoreValue(saved, submitted reflect.Value) {
	if !submitted.IsValid() {
		return
	}
	switch submitted.Kind() {
	case reflect.Pointer:
		if submitted.IsNil() {
			return
		}
		if saved.Kind() == reflect.Pointer && !saved.IsNil() {
			restoreValue(saved.Elem(), submitted.Elem())
		}
	case reflect.Struct:
		if submitted.Type() == secretType {
			// Only the mask is worth restoring. Raw is the string as written,
			// so any other value is what the client chose: a reference, a
			// literal, or an empty string that removes the secret.
			if submitted.Interface().(spec.Secret).Raw() != spec.Mask {
				return
			}
			if held, ok := savedSecret(saved); ok {
				restoreSecret(submitted, held)
			}
			return
		}
		if saved.Kind() != reflect.Struct {
			return
		}
		for i := range submitted.NumField() {
			if !submitted.Type().Field(i).IsExported() {
				continue
			}
			restoreValue(saved.Field(i), submitted.Field(i))
		}
	case reflect.Map:
		if saved.Kind() != reflect.Map {
			return
		}
		// A map value is a copy, so the restored entity is written back
		// instead of modified in place.
		for entries := submitted.MapRange(); entries.Next(); {
			key := entries.Key()
			entity := reflect.New(entries.Value().Type()).Elem()
			entity.Set(entries.Value())
			restoreValue(saved.MapIndex(key), entity)
			submitted.SetMapIndex(key, entity)
		}
	case reflect.Slice, reflect.Array:
		if saved.Kind() != submitted.Kind() || saved.Len() < submitted.Len() {
			return
		}
		for i := range submitted.Len() {
			restoreValue(saved.Index(i), submitted.Index(i))
		}
	}
}

// savedSecret reads the credential the files hold at a matching position, when
// there is one.
func savedSecret(saved reflect.Value) (spec.Secret, bool) {
	if !saved.IsValid() || saved.Type() != secretType {
		return spec.Secret{}, false
	}
	return saved.Interface().(spec.Secret), true
}

// restoreSecret puts the saved credential back into a masked field, keeping the
// reference it was written with so the file still reads the same way.
func restoreSecret(field reflect.Value, held spec.Secret) {
	if held.HasRef() {
		field.Set(reflect.ValueOf(spec.NewSecretRef(held.Raw(), held.Reveal())))
		return
	}
	field.Set(reflect.ValueOf(spec.NewSecret(held.Raw())))
}
