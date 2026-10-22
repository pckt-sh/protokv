package meilisearch

import (
	"strings"
	"sync"

	meilisearchv1 "github.com/pckt-sh/protokv/gen/go/protokv/meilisearch/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// OperationFunc transforms a field value, arg is the text after `=`
type OperationFunc func(value any, arg string) any

var operations sync.Map

func init() {
	RegisterOperation("split", split)
}

// RegisterOperation makes an operation usable in fields, it replaces
// any operation registered with the same name.
func RegisterOperation(name string, fn OperationFunc) {
	operations.Store(name, fn)
}

// split splits a string by arg, and keeps the original string
// when there is more than one part.
func split(value any, arg string) any {
	str, ok := value.(string)
	if !ok {
		return value
	}

	parts := strings.Split(str, arg)
	for idx, p := range parts {
		parts[idx] = strings.TrimSpace(p)
	}

	if len(parts) > 1 {
		parts = append(parts, str)
	}

	return parts
}

// Operation is a transformation operation
type Operation struct {
	Name     string
	Argument string
}

// Apply runs the operation, unknown operations return the value unchanged
func (op Operation) Apply(value any) any {
	fn, ok := operations.Load(op.Name)
	if !ok {
		return value
	}

	return fn.(OperationFunc)(value, op.Argument)
}

// Field is a field to index with potential operations
type Field struct {
	Name       string
	JSONPath   string
	Operations []Operation
}

// GetIndex returns the index option of a message, nil if it has none
func GetIndex(desc protoreflect.MessageDescriptor) *meilisearchv1.Index {
	opts := desc.Options()
	if !proto.HasExtension(opts, meilisearchv1.E_Index) {
		return nil
	}

	return proto.GetExtension(opts, meilisearchv1.E_Index).(*meilisearchv1.Index)
}

// ParseFields returns the fields of an index option
func ParseFields(idx *meilisearchv1.Index) []Field {
	if idx == nil {
		return nil
	}

	fields := make([]Field, 0, len(idx.Fields))
	for _, f := range idx.Fields {
		parts := strings.Split(f, ":")

		if len(parts) == 1 {
			fields = append(fields, Field{Name: parts[0], JSONPath: parts[0]})
			continue
		}

		field := Field{Name: parts[0], JSONPath: parts[1]}
		for _, part := range parts[2:] {
			opName, args, _ := strings.Cut(part, "=")
			field.Operations = append(field.Operations, Operation{opName, args})
		}

		fields = append(fields, field)
	}

	return fields
}
