package main

import (
	"bytes"
	"encoding/json"
	"strings"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

var jsonOpts = protojson.MarshalOptions{UseProtoNames: true}

// renderJSON indents with encoding/json, protojson output is
// intentionally unstable.
func renderJSON(msg proto.Message) string {
	b, err := jsonOpts.Marshal(msg)
	if err != nil {
		return err.Error()
	}

	var out bytes.Buffer
	if err := json.Indent(&out, b, "", "  "); err != nil {
		return string(b)
	}

	return out.String()
}

// highlightJSON colors keys, strings, numbers and literals of
// indented JSON.
func highlightJSON(src string, st styles) string {
	var out strings.Builder
	out.Grow(len(src) * 2)

	for i := 0; i < len(src); {
		c := src[i]

		switch {
		case c == '"':
			j := i + 1
			for j < len(src) && src[j] != '"' {
				if src[j] == '\\' {
					j++
				}
				j++
			}
			j = min(j+1, len(src))

			// A string followed by a colon is a key.
			k := j
			for k < len(src) && src[k] == ' ' {
				k++
			}

			if k < len(src) && src[k] == ':' {
				out.WriteString(st.jsonKey.Render(src[i:j]))
			} else {
				out.WriteString(st.jsonString.Render(src[i:j]))
			}
			i = j

		case c == '-' || (c >= '0' && c <= '9'):
			j := i + 1
			for j < len(src) && strings.IndexByte("0123456789.eE+-", src[j]) >= 0 {
				j++
			}
			out.WriteString(st.jsonNumber.Render(src[i:j]))
			i = j

		case strings.HasPrefix(src[i:], "true"), strings.HasPrefix(src[i:], "false"), strings.HasPrefix(src[i:], "null"):
			j := i + strings.IndexAny(src[i:]+",", ",\n}] ")
			out.WriteString(st.jsonLiteral.Render(src[i:j]))
			i = j

		case c == '{' || c == '}' || c == '[' || c == ']' || c == ':' || c == ',':
			out.WriteString(st.muted.Render(string(c)))
			i++

		default:
			out.WriteByte(c)
			i++
		}
	}

	return out.String()
}
