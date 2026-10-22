package schema

import (
	"errors"
	"slices"
	"sync"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
)

type Relation struct {
	// name is the field name (ex: `price`)
	name string

	// children storage
	storage Storage

	// descriptor is the embedded field (ex: `Product.price`)
	descriptor protoreflect.FieldDescriptor

	// msgType allows creating the children message
	msgType protoreflect.MessageType
}

// relations is a caching layer
var relations sync.Map

var (
	ErrNoRelation            = errors.New("relation does not exists")
	ErrRelationNotRegistered = errors.New("relation is not registered")
	ErrFieldUnknown          = errors.New("no field for this relation")
)

func (s Storage) GetRelation(name string) (Relation, error) {
	if rel, ok := relations.Load(string(s.name) + ":" + name); ok {
		if v, ok := rel.(Relation); ok {
			return v, nil
		}

		return Relation{}, ErrNoRelation
	}

	if !slices.Contains(s.store.Relations, name) {
		return Relation{}, ErrRelationNotRegistered
	}

	field := s.descriptor.Fields().ByTextName(name)
	if field == nil {
		return Relation{}, ErrFieldUnknown
	}

	if field.IsList() || field.IsMap() {
		return Relation{}, ErrNotAMessage
	}

	msg := field.Message()
	if msg == nil {
		return Relation{}, ErrNotAMessage
	}

	msgType, err := protoregistry.GlobalTypes.FindMessageByName(msg.FullName())
	if err != nil {
		return Relation{}, ErrNoRelation
	}

	storage, err := Get(msg.FullName())
	if err != nil {
		return Relation{}, err
	}

	rel := Relation{
		name:       name,
		storage:    storage,
		descriptor: field,
		msgType:    msgType,
	}
	relations.Store(string(s.name)+":"+name, rel)

	return rel, nil
}

// GetName returns the relation field name
func (r Relation) GetName() string {
	return r.name
}

// GetStorage returns the storage of the related message
func (r Relation) GetStorage() Storage {
	return r.storage
}

// GetKey returns the relation-formatted key
func (r Relation) GetKey(pk string, options map[string]string) string {
	return r.storage.GetKey(pk, options)
}

// Assign unmarshal and sets the relation on a message
func (r Relation) Assign(msg proto.Message, buf []byte) error {
	rft := msg.ProtoReflect()

	subMsg := r.msgType.New()
	if len(buf) == 0 { // Set an empty message if buf is empty
		rft.Set(r.descriptor, protoreflect.ValueOfMessage(subMsg))

		return nil
	}

	if err := proto.Unmarshal(buf, subMsg.Interface()); err != nil {
		return err
	}

	rft.Set(r.descriptor, protoreflect.ValueOfMessage(subMsg))
	return nil
}
