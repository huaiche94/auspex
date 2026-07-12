package config

import "errors"

// ErrInvalidSchemaVersion is returned when the merged configuration is
// missing schema_version or declares a value other than SchemaVersion.
var ErrInvalidSchemaVersion = errors.New("config: invalid schema_version")

// ErrUnknownFields is returned under StrictUnknownFields when the merged
// configuration has top-level keys this package does not recognize.
var ErrUnknownFields = errors.New("config: unknown fields")

// ErrDuplicateSource is returned when Load receives two Layer values for
// the same Source.
var ErrDuplicateSource = errors.New("config: duplicate layer source")
