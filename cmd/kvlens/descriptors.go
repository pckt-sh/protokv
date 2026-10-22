package main

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/pckt-sh/protokv/schema"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

// loadDescriptors registers the files of FileDescriptorSets (or buf images,
// `buf build -o models.binpb`) in the global registries, so schema and
// protojson can resolve their messages as dynamic types.
func loadDescriptors(paths []string) error {
	for _, path := range paths {
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		var set descriptorpb.FileDescriptorSet
		if err := (proto.UnmarshalOptions{Resolver: protoregistry.GlobalTypes}).Unmarshal(b, &set); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}

		files := make(map[string]*descriptorpb.FileDescriptorProto, len(set.File))
		for _, f := range set.File {
			files[f.GetName()] = f
		}

		for _, f := range set.File {
			if err := registerFile(f, files); err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
		}
	}

	return nil
}

// registerFile registers a file after its dependencies. Files already known,
// like the compiled-in storage options, are kept as they are.
func registerFile(f *descriptorpb.FileDescriptorProto, files map[string]*descriptorpb.FileDescriptorProto) error {
	if _, err := protoregistry.GlobalFiles.FindFileByPath(f.GetName()); err == nil {
		return nil
	}

	for _, dep := range f.GetDependency() {
		if d, ok := files[dep]; ok {
			if err := registerFile(d, files); err != nil {
				return err
			}
		}
	}

	fd, err := protodesc.NewFile(f, protoregistry.GlobalFiles)
	if err != nil {
		return fmt.Errorf("%s: %w", f.GetName(), err)
	}

	if err := protoregistry.GlobalFiles.RegisterFile(fd); err != nil {
		return fmt.Errorf("%s: %w", f.GetName(), err)
	}

	registerTypes(fd.Messages(), fd.Enums(), fd.Extensions())
	return nil
}

func registerTypes(msgs protoreflect.MessageDescriptors, enums protoreflect.EnumDescriptors, exts protoreflect.ExtensionDescriptors) {
	// Conflicts are ignored, the type registered first wins.
	for i := range enums.Len() {
		_ = protoregistry.GlobalTypes.RegisterEnum(dynamicpb.NewEnumType(enums.Get(i)))
	}

	for i := range exts.Len() {
		_ = protoregistry.GlobalTypes.RegisterExtension(dynamicpb.NewExtensionType(exts.Get(i)))
	}

	for i := range msgs.Len() {
		md := msgs.Get(i)
		_ = protoregistry.GlobalTypes.RegisterMessage(dynamicpb.NewMessageType(md))
		registerTypes(md.Messages(), md.Enums(), md.Extensions())
	}
}

// storedModels lists every registered message with a storage option.
func storedModels() []schema.Storage {
	var models []schema.Storage

	var walk func(msgs protoreflect.MessageDescriptors)
	walk = func(msgs protoreflect.MessageDescriptors) {
		for i := range msgs.Len() {
			md := msgs.Get(i)
			if s, err := schema.Get(md.FullName()); err == nil {
				models = append(models, s)
			}
			walk(md.Messages())
		}
	}

	protoregistry.GlobalFiles.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		walk(fd.Messages())
		return true
	})

	slices.SortFunc(models, func(a, b schema.Storage) int {
		return strings.Compare(string(a.GetName()), string(b.GetName()))
	})

	return models
}
