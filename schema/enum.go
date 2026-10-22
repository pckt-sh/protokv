package schema

import (
	"sync"

	protokvv1 "github.com/pckt-sh/protokv/gen/go/protokv/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
)

var enums sync.Map

// Enum maps the values of an enum to their names in keys
type Enum struct {
	name   protoreflect.FullName
	values map[protoreflect.EnumNumber]string
}

// GetName returns the enum full name
func (e Enum) GetName() protoreflect.FullName {
	return e.name
}

// ValueName returns the name of a value: its (protokv.v1.value).name
// option if set, its full name otherwise.
func (e Enum) ValueName(n protoreflect.EnumNumber) string {
	return e.values[n]
}

// GetEnum retrieves a parsed and/or cached enum
func GetEnum(name protoreflect.FullName) (Enum, error) {
	if val, ok := enums.Load(name); ok {
		return val.(Enum), nil
	}

	enumType, err := protoregistry.GlobalTypes.FindEnumByName(name)
	if err != nil {
		return Enum{}, err
	}

	enum := Enum{
		name:   name,
		values: map[protoreflect.EnumNumber]string{},
	}

	values := enumType.Descriptor().Values()
	for idx := range values.Len() {
		value := values.Get(idx)
		valueName := string(value.FullName())

		if proto.HasExtension(value.Options(), protokvv1.E_Value) {
			if name := proto.GetExtension(value.Options(), protokvv1.E_Value).(*protokvv1.EnumValue).GetName(); name != "" {
				valueName = name
			}
		}

		enum.values[value.Number()] = valueName
	}

	enums.Store(name, enum)

	return enum, nil
}
