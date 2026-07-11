package registrar

// systemSpec documents the endpoints the registrar itself serves plus the
// /health convention every composition mounts. Paths are relative to the
// base path.
var systemSpec = []byte(`{
	"tags": [{"name": "system", "description": "Service discovery and health"}],
	"paths": {
		"/capabilities": {
			"get": {
				"tags": ["system"],
				"summary": "Get capabilities",
				"description": "Returns the list of enabled service capabilities",
				"produces": ["application/json"],
				"responses": {"200": {"description": "List of enabled capabilities", "schema": {"type": "array", "items": {"type": "string"}}}}
			}
		},
		"/health": {
			"get": {
				"tags": ["system"],
				"summary": "Health check",
				"description": "Returns health status; compositions may include version, uptime, and block height",
				"produces": ["application/json"],
				"responses": {"200": {"description": "Health status", "schema": {"type": "object"}}}
			}
		}
	}
}`)
