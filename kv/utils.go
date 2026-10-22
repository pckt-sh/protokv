package kv

import (
	"errors"
	"strconv"
)

var ErrNotFound = errors.New("object not found")

type Number interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 |
		~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 | ~uintptr |
		~float32 | ~float64
}

type KeyValue interface {
	~string |
		~int | ~int8 | ~int16 | ~int32 | ~int64 |
		~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 | ~uintptr |
		~float32 | ~float64
}

func formatString[T KeyValue](v T) string {
	buf := make([]byte, 0, 32)

	switch v := any(v).(type) {
	case string:
		return v

	case int:
		buf = strconv.AppendInt(buf, int64(v), 10)
	case int8:
		buf = strconv.AppendInt(buf, int64(v), 10)
	case int16:
		buf = strconv.AppendInt(buf, int64(v), 10)
	case int32:
		buf = strconv.AppendInt(buf, int64(v), 10)
	case int64:
		buf = strconv.AppendInt(buf, v, 10)

	case uint:
		buf = strconv.AppendUint(buf, uint64(v), 10)
	case uint8:
		buf = strconv.AppendUint(buf, uint64(v), 10)
	case uint16:
		buf = strconv.AppendUint(buf, uint64(v), 10)
	case uint32:
		buf = strconv.AppendUint(buf, uint64(v), 10)
	case uint64:
		buf = strconv.AppendUint(buf, v, 10)
	case uintptr:
		buf = strconv.AppendUint(buf, uint64(v), 10)

	case float32:
		buf = strconv.AppendInt(buf, int64(v), 10)
	case float64:
		buf = strconv.AppendInt(buf, int64(v), 10)
	}

	return string(buf)
}
