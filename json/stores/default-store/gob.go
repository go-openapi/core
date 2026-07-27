package store

import (
	"bytes"
	"encoding/gob"
)

type encodableStore struct {
	Options options
	Arena   []byte
}

func (s Store) MarshalBinary() ([]byte, error) {
	source := encodableStore{
		Options: s.options,
		Arena:   s.arena,
	}

	return gobEncode(source)
}

func (s *Store) UnmarshalBinary(data []byte) error {
	var target encodableStore

	decoder := gob.NewDecoder(bytes.NewReader(data))
	err := decoder.Decode(&target)
	if err != nil {
		return err
	}

	s.options = target.Options
	s.arena = target.Arena

	return nil
}

type encodableOptions struct {
	CompressionOptions compressionOptions
	EnableCompression  bool
	MinArenaSize       int
}

func (o options) MarshalBinary() ([]byte, error) {
	source := encodableOptions{
		CompressionOptions: o.compressionOptions,
		EnableCompression:  o.enableCompression,
		MinArenaSize:       o.minArenaSize,
	}

	return gobEncode(source)
}

func (o *options) UnmarshalBinary(data []byte) error {
	var target encodableOptions

	decoder := gob.NewDecoder(bytes.NewReader(data))
	err := decoder.Decode(&target)
	if err != nil {
		return err
	}

	o.compressionOptions = target.CompressionOptions
	o.enableCompression = target.EnableCompression
	o.minArenaSize = target.MinArenaSize

	// The cached compression writer cw is not serialized (it is a derived artifact, and flate writers
	// are not gob-encodable): it rebuilds lazily from the restored (level, dict) on the next
	// compression (see [compressionOptions.compressWriter]).

	return nil
}

type encodableCompressionOptions struct {
	CompressionThreshold int
	CompressionLevel     int
	Dict                 []byte
}

func (o compressionOptions) MarshalBinary() ([]byte, error) {
	source := encodableCompressionOptions{
		CompressionThreshold: o.compressionThreshold,
		CompressionLevel:     o.compressionLevel,
		Dict:                 o.dict,
	}

	return gobEncode(source)
}

func (o *compressionOptions) UnmarshalBinary(data []byte) error {
	var target encodableCompressionOptions

	decoder := gob.NewDecoder(bytes.NewReader(data))
	err := decoder.Decode(&target)
	if err != nil {
		return err
	}

	o.compressionThreshold = target.CompressionThreshold
	o.compressionLevel = target.CompressionLevel
	o.dict = target.Dict

	return nil
}

func gobEncode(source any) ([]byte, error) {
	var out bytes.Buffer
	encoder := gob.NewEncoder(&out)

	err := encoder.Encode(source)
	if err != nil {
		return nil, err
	}

	return out.Bytes(), nil
}
