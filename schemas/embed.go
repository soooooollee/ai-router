package schemas

import _ "embed"

// ConfigV1 is the authoritative configuration schema used by both runtime
// validation and external tooling.
//
//go:embed config.v1.schema.json
var ConfigV1 []byte
