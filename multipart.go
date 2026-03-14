package lolzteam

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"reflect"
	"strings"
)

// FileUpload represents a file to upload via multipart/form-data.
type FileUpload struct {
	Filename string
	Data     io.Reader
}

// multipartBody holds fields and files for a multipart/form-data request.
type multipartBody struct {
	fields map[string]string
	files  map[string]fileField
}

type fileField struct {
	filename string
	data     io.Reader
}

// encode writes the multipart body into a byte slice and returns it along with the content type.
func (mb *multipartBody) encode() (data []byte, contentType string, err error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)

	for name, value := range mb.fields {
		if err := w.WriteField(name, value); err != nil {
			return nil, "", fmt.Errorf("failed to write multipart field %q: %w", name, err)
		}
	}

	for name, file := range mb.files {
		part, err := w.CreateFormFile(name, file.filename)
		if err != nil {
			return nil, "", fmt.Errorf("failed to create multipart file %q: %w", name, err)
		}
		if _, err := io.Copy(part, file.data); err != nil {
			return nil, "", fmt.Errorf("failed to write multipart file %q: %w", name, err)
		}
	}

	if err := w.Close(); err != nil {
		return nil, "", fmt.Errorf("failed to close multipart writer: %w", err)
	}

	return buf.Bytes(), w.FormDataContentType(), nil
}

var fileUploadType = reflect.TypeOf(FileUpload{})

// structToMultipart converts a struct pointer to a multipartBody.
// Uses `form` struct tags. Fields of type FileUpload or *FileUpload become file parts.
// Other fields become text parts.
func structToMultipart(v any) *multipartBody {
	mb := &multipartBody{
		fields: make(map[string]string),
		files:  make(map[string]fileField),
	}

	if v == nil {
		return mb
	}

	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Ptr {
		if rv.IsNil() {
			return mb
		}
		rv = rv.Elem()
	}

	if rv.Kind() != reflect.Struct {
		return mb
	}

	rt := rv.Type()
	for i := range rt.NumField() {
		field := rt.Field(i)
		tag := field.Tag.Get("form")
		if tag == "" || tag == "-" {
			continue
		}

		name, _, _ := strings.Cut(tag, ",")
		fieldVal := rv.Field(i)

		appendMultipartField(mb, name, field.Type, fieldVal)
	}

	return mb
}

func appendMultipartField(mb *multipartBody, name string, fieldType reflect.Type, fieldVal reflect.Value) {
	// Dereference pointer
	if fieldType.Kind() == reflect.Ptr {
		if fieldVal.IsNil() {
			return
		}
		fieldType = fieldType.Elem()
		fieldVal = fieldVal.Elem()
	}

	// Check if it's a FileUpload
	if fieldType == fileUploadType {
		fu := fieldVal.Interface().(FileUpload)
		if fu.Data != nil {
			mb.files[name] = fileField{
				filename: fu.Filename,
				data:     fu.Data,
			}
		}
		return
	}

	// Regular field → text part
	switch fieldVal.Kind() {
	case reflect.String:
		mb.fields[name] = fieldVal.String()
	case reflect.Int, reflect.Int64:
		mb.fields[name] = fmt.Sprintf("%d", fieldVal.Int())
	case reflect.Float64:
		mb.fields[name] = fmt.Sprintf("%g", fieldVal.Float())
	case reflect.Bool:
		if fieldVal.Bool() {
			mb.fields[name] = "1"
		} else {
			mb.fields[name] = "0"
		}
	}
}
