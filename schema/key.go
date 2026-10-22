package schema

import (
	"strings"
	"sync"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// primaryKeys cache the fieldDescriptor to access PK
var primaryKeys sync.Map

// GetKey returns a valid key by replacing
// placeholders in the prefix.
//
// Example:
//   - Prefix: `price:{region}:{pk}`
//   - {pk} gets replaced by `pk` parameter
//   - {region} gets replaced by option keys
func (s Storage) GetKey(pk string, options map[string]string) string {
	key := strings.ReplaceAll(s.store.Prefix, "{pk}", pk)

	for placeholder, value := range options {
		key = strings.ReplaceAll(key, "{"+placeholder+"}", value)
	}

	return key
}

// GetKeys allows formatting multiple keys at once.
//
// It uses `GetKey`.
func (s Storage) GetKeys(pks []string, options map[string]string) []string {
	results := make([]string, 0, len(pks))
	for _, pk := range pks {
		results = append(results, s.GetKey(pk, options))
	}

	return results
}

// TrimPrefix returns key after last `:`
func (s Storage) TrimPrefix(key string) string {
	sep := strings.LastIndex(key, ":")
	if sep > 0 {
		return key[sep+1:]
	}

	return key
}

// TrimPrefixes trim all prefixes at once
func (s Storage) TrimPrefixes(keys []string) []string {
	results := make([]string, 0, len(keys))
	for _, k := range keys {
		results = append(results, s.TrimPrefix(k))
	}

	return results
}

// GetPrefix returns the message key template
func (s Storage) GetPrefix() string {
	return s.store.Prefix
}

// GetPlaceholders extract placeholders
// inside brackets.
//
// Example:
//
//	-> Input: `price:{region}:{pk}`
//	-> Output: []string{"region", "pk"}
func GetPlaceholders(s string) []string {
	var result []string

	for {
		start := strings.IndexByte(s, '{')
		if start == -1 {
			break
		}

		end := strings.IndexByte(s[start+1:], '}')
		if end == -1 {
			break
		}
		end += start + 1

		result = append(result, s[start+1:end])
		s = s[end+1:]
	}

	return result
}

// GetPrimaryKey finds the primary key of a `proto.Message`
func (s Storage) GetPrimaryKey(msg proto.Message) string {
	rft := msg.ProtoReflect()

	var field protoreflect.FieldDescriptor
	if val, ok := primaryKeys.Load(s.name); ok {
		field = val.(protoreflect.FieldDescriptor)
	} else {
		field = rft.Descriptor().Fields().ByTextName(s.store.PrimaryKey)
		primaryKeys.Store(s.name, field)
	}

	return rft.Get(field).String()
}

// segment is a part of a key template: a literal text,
// or a field to read from the message when field is set.
type segment struct {
	literal string
	field   protoreflect.FieldDescriptor
}

// templates caches the parsed prefix of each message
var templates sync.Map

// template splits the prefix into segments, once per message.
// Placeholders without a field are kept as literal text.
func (s Storage) template() []segment {
	if val, ok := templates.Load(s.name); ok {
		return val.([]segment)
	}

	var segments []segment
	rest := s.store.Prefix

	for _, pc := range GetPlaceholders(rest) {
		token := "{" + pc + "}"
		idx := strings.Index(rest, token)
		segments = append(segments, segment{literal: rest[:idx]})
		rest = rest[idx+len(token):]

		fieldName := pc
		if fieldName == "pk" {
			fieldName = s.store.PrimaryKey
		}

		if field := s.descriptor.Fields().ByTextName(fieldName); field != nil {
			segments = append(segments, segment{field: field})
		} else {
			segments = append(segments, segment{literal: token})
		}
	}
	segments = append(segments, segment{literal: rest})

	templates.Store(s.name, segments)
	return segments
}

// fieldValue formats a field for a key, enums use their custom name.
func fieldValue(rft protoreflect.Message, field protoreflect.FieldDescriptor) string {
	if field.Kind() != protoreflect.EnumKind {
		return rft.Get(field).String()
	}

	enum, err := GetEnum(field.Enum().FullName())
	if err != nil {
		return string(field.Enum().Values().ByNumber(rft.Get(field).Enum()).FullName())
	}

	return enum.values[rft.Get(field).Enum()]
}

// ToKey builds the key of a message by filling its prefix template.
func (s Storage) ToKey(msg proto.Message) string {
	rft := msg.ProtoReflect()

	var key strings.Builder
	key.Grow(len(s.store.Prefix) + 32)

	for _, seg := range s.template() {
		if seg.field == nil {
			key.WriteString(seg.literal)
		} else {
			key.WriteString(fieldValue(rft, seg.field))
		}
	}

	return key.String()
}
