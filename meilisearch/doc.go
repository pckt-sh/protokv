// Package meilisearch syncs messages stored with package kv into
// Meilisearch, following their (protokv.meilisearch.v1.index) option.
//
// Each entry of `fields` is `<name>[:<json_path>[:<op>[=<arg>]]...]`. The
// JSON path is a gjson path over the protojson encoding of the message
// (proto field names). Operations transform the value in order; `split` is
// built in and others can be added with RegisterOperation.
package meilisearch
