package openapi

import _ "embed"

// Spec contains the canonical OpenAPI contract embedded into the API binary.
//
//go:embed openapi.yaml
var Spec []byte
