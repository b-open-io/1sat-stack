// Package docs embeds this service's OpenAPI fragment for the registrar.
package docs

import _ "embed"

//go:embed swagger.json
var Spec []byte
