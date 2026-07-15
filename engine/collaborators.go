package engine

import (
	"fmt"
	"reflect"
)

func validateCollaborator(name string, value any) error {
	if value == nil {
		return fmt.Errorf("%s cannot be nil", name)
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		if v.IsNil() {
			return fmt.Errorf("%s cannot be nil", name)
		}
	}
	return nil
}

func validateStreamingCollaborators(idx RightIndex, writer ResultWriter) error {
	if err := validateCollaborator("right index", idx); err != nil {
		return err
	}
	return validateCollaborator("result writer", writer)
}
