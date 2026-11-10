package httpserver

import (
	"context"
	"fmt"
	"reflect"

	"github.com/go-playground/mold/v4/modifiers"
)

var defaultModifier = newDefaultModifier()

// Modifier applies post-binding transformations to request input structs.
type Modifier interface {
	Struct(ctx context.Context, v any) error
}

// WithCustomModifier replaces the package-level input modifier used after binding.
func WithCustomModifier(modifier Modifier) {
	defaultModifier = modifier
}

func newDefaultModifier() Modifier {
	mod := modifiers.New()
	mod.SetTagName("mold")

	return mod
}

func modifyInput(ctx context.Context, input any) error {
	if input == nil {
		return nil
	}

	value := reflect.ValueOf(input)
	if value.Kind() == reflect.Ptr {
		if value.IsNil() {
			return nil
		}

		value = value.Elem()
	}

	if value.Kind() != reflect.Struct {
		return nil
	}

	err := defaultModifier.Struct(ctx, input)
	if err != nil {
		return fmt.Errorf("failed to modify input: %w", err)
	}

	return err
}
