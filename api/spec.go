package api

import _ "embed"

// OpenAPI contains the public API contract.
//
//go:embed openapi.yaml
var OpenAPI []byte
