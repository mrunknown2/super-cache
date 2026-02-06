package supercache

import (
	"encoding/json"

	"github.com/vmihailenco/msgpack/v5"
)

// Serializer defines the interface for cache value serialization.
type Serializer interface {
	Marshal(v any) ([]byte, error)
	Unmarshal(data []byte, v any) error
}

// JSONSerializer implements Serializer using JSON encoding.
type JSONSerializer struct{}

// Marshal serializes value to JSON.
func (JSONSerializer) Marshal(v any) ([]byte, error) {
	return json.Marshal(v)
}

// Unmarshal deserializes JSON data to value.
func (JSONSerializer) Unmarshal(data []byte, v any) error {
	return json.Unmarshal(data, v)
}

// MsgPackSerializer implements Serializer using MessagePack encoding.
// MessagePack is more compact and faster than JSON.
type MsgPackSerializer struct{}

// Marshal serializes value to MessagePack.
func (MsgPackSerializer) Marshal(v any) ([]byte, error) {
	return msgpack.Marshal(v)
}

// Unmarshal deserializes MessagePack data to value.
func (MsgPackSerializer) Unmarshal(data []byte, v any) error {
	return msgpack.Unmarshal(data, v)
}
