package schema

import (
	"errors"
	"sync"

	protokvv1 "github.com/pckt-sh/protokv/gen/go/protokv/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
)

// cache stores a parsed storage
var cache sync.Map

var (
	ErrNoStorage    = errors.New("message does not has storage extension")
	ErrParsingError = errors.New("cannot parse storage extension")
	ErrNotAMessage  = errors.New("field is not a message")
	ErrNoPrimaryKey = errors.New("primary key is not a field of the message")
)

// Storage is the parsed storage option of a message
type Storage struct {
	// The message full name
	name protoreflect.FullName

	// store is the original *protokvv1.Storage extension
	store *protokvv1.Storage

	// descriptor is the message descriptor
	descriptor protoreflect.MessageDescriptor
}

// GetName returns the message full name
func (s Storage) GetName() protoreflect.FullName {
	return s.name
}

func (s Storage) GetDescriptor() protoreflect.MessageDescriptor {
	return s.descriptor
}

// GetOptions returns the raw storage option
func (s Storage) GetOptions() *protokvv1.Storage {
	return s.store
}

// Get a Storage by the message full name
func Get(name protoreflect.FullName) (Storage, error) {
	if store, ok := cache.Load(name); ok {
		if v, ok := store.(Storage); ok {
			return v, nil
		}

		return Storage{}, ErrNoStorage
	}

	msgType, err := protoregistry.GlobalTypes.FindMessageByName(name)
	if err != nil {
		return Storage{}, err
	}

	opts := msgType.Descriptor().Options()

	if !proto.HasExtension(opts, protokvv1.E_Storage) {
		return Storage{}, ErrNoStorage
	}

	ext := proto.GetExtension(opts, protokvv1.E_Storage).(*protokvv1.Storage)
	if ext == nil {
		return Storage{}, ErrParsingError
	}

	if msgType.Descriptor().Fields().ByTextName(ext.PrimaryKey) == nil {
		return Storage{}, ErrNoPrimaryKey
	}

	store := Storage{
		name:       name,
		store:      ext,
		descriptor: msgType.Descriptor(),
	}
	cache.Store(name, store)

	return store, nil
}

// GetFromMsg retrieves the storage via proto.Message
func GetFromMsg(msg proto.Message) (Storage, error) {
	fullName := msg.ProtoReflect().Descriptor().FullName()

	return Get(fullName)
}

// GetFromType uses generics to retrieve the storage
func GetFromType[T proto.Message]() (Storage, error) {
	var msg T

	return GetFromMsg(msg)
}
