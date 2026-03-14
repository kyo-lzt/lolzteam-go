package lolzteam

import (
	"fmt"
	"net/url"
	"reflect"
	"strings"
)

// structToQuery converts a struct pointer to url.Values using `query` struct tags.
// Nil pointer fields are skipped.
func structToQuery(v any) url.Values {
	return structToValues(v, "query")
}

// structToForm converts a struct pointer to url.Values using `form` struct tags.
// Nil pointer fields are skipped.
func structToForm(v any) url.Values {
	return structToValues(v, "form")
}

func structToValues(v any, tagName string) url.Values {
	values := url.Values{}
	if v == nil {
		return values
	}

	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Ptr {
		if rv.IsNil() {
			return values
		}
		rv = rv.Elem()
	}

	if rv.Kind() != reflect.Struct {
		return values
	}

	rt := rv.Type()
	for i := range rt.NumField() {
		field := rt.Field(i)
		tag := field.Tag.Get(tagName)
		if tag == "" || tag == "-" {
			continue
		}

		// Handle comma-separated tag options (e.g. `query:"name,omitempty"`)
		name, _, _ := strings.Cut(tag, ",")

		fieldVal := rv.Field(i)
		appendFieldValues(&values, name, fieldVal)
	}

	return values
}

func appendFieldValues(values *url.Values, name string, fieldVal reflect.Value) {
	switch fieldVal.Kind() {
	case reflect.Ptr:
		if fieldVal.IsNil() {
			return
		}
		appendFieldValues(values, name, fieldVal.Elem())

	case reflect.String:
		values.Set(name, fieldVal.String())

	case reflect.Int, reflect.Int64:
		values.Set(name, fmt.Sprintf("%d", fieldVal.Int()))

	case reflect.Float64:
		values.Set(name, fmt.Sprintf("%g", fieldVal.Float()))

	case reflect.Bool:
		if fieldVal.Bool() {
			values.Set(name, "1")
		} else {
			values.Set(name, "0")
		}

	case reflect.Slice:
		for j := range fieldVal.Len() {
			elem := fieldVal.Index(j)
			switch elem.Kind() {
			case reflect.String:
				values.Add(name, elem.String())
			case reflect.Int, reflect.Int64:
				values.Add(name, fmt.Sprintf("%d", elem.Int()))
			case reflect.Float64:
				values.Add(name, fmt.Sprintf("%g", elem.Float()))
			}
		}

	case reflect.Map:
		for _, key := range fieldVal.MapKeys() {
			keyStr := fmt.Sprintf("%s[%s]", name, key)
			val := fieldVal.MapIndex(key)
			if val.Kind() == reflect.Interface {
				val = val.Elem()
			}
			switch val.Kind() {
			case reflect.String:
				values.Set(keyStr, val.String())
			case reflect.Int, reflect.Int64:
				values.Set(keyStr, fmt.Sprintf("%d", val.Int()))
			case reflect.Float64:
				values.Set(keyStr, fmt.Sprintf("%g", val.Float()))
			case reflect.Bool:
				if val.Bool() {
					values.Set(keyStr, "1")
				} else {
					values.Set(keyStr, "0")
				}
			}
		}

	case reflect.Interface:
		if !fieldVal.IsNil() {
			appendFieldValues(values, name, fieldVal.Elem())
		}
	}
}
