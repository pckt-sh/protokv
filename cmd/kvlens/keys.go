package main

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/pckt-sh/protokv/kv"
	"github.com/pckt-sh/protokv/schema"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

const (
	// pageSize is how many keys a page loads at least, unless the scan ends.
	pageSize = 100
	// scanCount is the COUNT hint of each SCAN call.
	scanCount = 1_000
	// maxScanCalls bounds a page when few keys match.
	maxScanCalls = 50
	timeout      = 10 * time.Second
)

var placeholderRe = regexp.MustCompile(`\{[^}]+\}`)

// scanPattern is the SCAN glob of a model: the pk is the filter, and
// placeholders without an option match anything.
func scanPattern(s schema.Storage, options map[string]string, filter string) string {
	pk := "*"
	if filter != "" {
		pk = filter
		if !strings.ContainsAny(filter, "*?[") {
			pk = "*" + filter + "*"
		}
	}

	return placeholderRe.ReplaceAllString(s.GetKey(pk, options), "*")
}

// keyMatcher matches the keys of a model only. Globs are too loose, the
// pattern `product:*` also matches `product:rank:1`.
func keyMatcher(s schema.Storage, options map[string]string) *regexp.Regexp {
	prefix := s.GetPrefix()

	var b strings.Builder
	b.WriteString("^")

	rest := prefix
	for _, loc := range placeholderRe.FindAllStringIndex(prefix, -1) {
		offset := len(prefix) - len(rest)
		b.WriteString(regexp.QuoteMeta(rest[:loc[0]-offset]))

		name := prefix[loc[0]+1 : loc[1]-1]
		if v, ok := options[name]; ok && name != "pk" {
			b.WriteString(regexp.QuoteMeta(v))
		} else {
			b.WriteString(`[^:]+`)
		}

		rest = prefix[loc[1]:]
	}
	b.WriteString(regexp.QuoteMeta(rest))
	b.WriteString("$")

	return regexp.MustCompile(b.String())
}

type page struct {
	keys   []string
	cursor uint64
	done   bool
}

// scanPage loads the next keys of a model from cursor.
func scanPage(db kv.Client, s schema.Storage, options map[string]string, filter string, cursor uint64) (page, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	if name, value, ok := strings.Cut(filter, "="); ok && slices.Contains(s.GetOptions().GetIndexes(), name) {
		pks, err := s.ResolveIndexes(ctx, db.Conn(), []string{name}, []string{value})
		if err != nil {
			return page{}, err
		}

		return page{keys: s.GetKeys(pks, options), done: true}, nil
	}

	var (
		pattern = scanPattern(s, options, filter)
		matcher = keyMatcher(s, options)
		p       = page{cursor: cursor}
	)

	for range maxScanCalls {
		keys, next, err := db.Scan(ctx, pattern, scanCount, p.cursor)
		if err != nil {
			return page{}, err
		}

		for _, k := range keys {
			if matcher.MatchString(k) {
				p.keys = append(p.keys, k)
			}
		}

		p.cursor = next
		if next == 0 {
			p.done = true
			break
		}

		if len(p.keys) >= pageSize {
			break
		}
	}

	return p, nil
}

// tab is a message shown in the sheet: the record itself or one relation.
type tab struct {
	name     string
	key      string
	storage  schema.Storage
	relation bool
	size     int
	json     string
	err      error
}

// loadRecord reads a message and its relations.
func loadRecord(db kv.Client, s schema.Storage, key string, options map[string]string) []tab {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	root := tab{name: shortName(s.GetName()), key: key, storage: s}
	msg, size, err := readMessage(ctx, db, s, key)
	root.size = size
	if err != nil {
		root.err = err
		return []tab{root}
	}
	root.json = renderJSON(msg)

	tabs := []tab{root}
	pk := s.GetPrimaryKey(msg)

	for _, name := range s.GetOptions().GetRelations() {
		t := tab{name: name, relation: true}

		rel, err := s.GetRelation(name)
		if err != nil {
			t.err = err
			tabs = append(tabs, t)
			continue
		}

		t.storage = rel.GetStorage()
		t.key = rel.GetKey(pk, fillOptions(rel.GetStorage(), msg, options))

		if strings.Contains(t.key, "{") {
			t.err = errMissingOptions(t.key)
		} else {
			relMsg, size, err := readMessage(ctx, db, t.storage, t.key)
			t.size = size
			if err != nil {
				t.err = err
			} else {
				t.json = renderJSON(relMsg)
			}
		}

		tabs = append(tabs, t)
	}

	return tabs
}

func readMessage(ctx context.Context, db kv.Client, s schema.Storage, key string) (proto.Message, int, error) {
	raw, err := db.Get(ctx, key)
	if errors.Is(err, kv.ErrNotFound) {
		return nil, 0, fmt.Errorf("No value stored at %s.", key)
	}
	if err != nil {
		return nil, 0, err
	}

	msg := dynamicpb.NewMessage(s.GetDescriptor())
	if err := proto.Unmarshal([]byte(raw), msg); err != nil {
		return nil, len(raw), err
	}

	return msg, len(raw), nil
}

// fillOptions completes options with the fields of msg named like the
// placeholders of s, so `price:{region}:{pk}` takes the region of the record.
func fillOptions(s schema.Storage, msg proto.Message, options map[string]string) map[string]string {
	filled := make(map[string]string, len(options))
	for k, v := range options {
		filled[k] = v
	}

	rft := msg.ProtoReflect()
	for _, name := range schema.GetPlaceholders(s.GetPrefix()) {
		if _, ok := filled[name]; ok || name == "pk" {
			continue
		}

		field := rft.Descriptor().Fields().ByTextName(name)
		if field == nil || field.IsList() || field.IsMap() {
			continue
		}

		if field.Kind() == protoreflect.EnumKind {
			if enum, err := schema.GetEnum(field.Enum().FullName()); err == nil {
				filled[name] = enum.ValueName(rft.Get(field).Enum())
				continue
			}
		}

		filled[name] = rft.Get(field).String()
	}

	return filled
}

type errMissingOptions string

func (e errMissingOptions) Error() string {
	return "key " + string(e) + " has placeholders without a value, set them with `o`"
}

func shortName(name protoreflect.FullName) string {
	return string(name.Name())
}

// parseOptions reads `a=1, b=2`.
func parseOptions(s string) map[string]string {
	options := map[string]string{}
	for part := range strings.SplitSeq(s, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if ok && strings.TrimSpace(k) != "" {
			options[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}

	return options
}

func formatOptions(options map[string]string) string {
	parts := make([]string, 0, len(options))
	for k, v := range options {
		parts = append(parts, k+"="+v)
	}
	slices.Sort(parts)

	return strings.Join(parts, ", ")
}
