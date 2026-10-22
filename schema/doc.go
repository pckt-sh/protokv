// Package schema reads the protokv.v1 options of protobuf messages:
// key templates, secondary indexes, relations and enum value names.
//
// Parsed options are cached per message, so lookups are cheap after the
// first call.
package schema
