package registrar

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/gofiber/fiber/v2"
)

// rootAnchoredPaths are fragment path roots that are never rebased onto a
// mount prefix: /.well-known/* is root-anchored by RFC 8615, and /content is
// the ordfs protocol's root content mount.
var rootAnchoredPaths = []string{"/.well-known", "/content"}

// DocInfo identifies the composition in the merged OpenAPI document.
type DocInfo struct {
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Version     string `json:"version,omitempty"`
}

// SetDocInfo sets the info block of the merged OpenAPI document. Call before
// Finalize.
func (r *Registrar) SetDocInfo(info DocInfo) {
	r.docInfo = &info
}

// swaggerFragment is the subset of an OpenAPI 2.0 document the merge handles.
type swaggerFragment struct {
	Swagger             string                     `json:"swagger,omitempty"`
	Info                *DocInfo                   `json:"info,omitempty"`
	BasePath            string                     `json:"basePath,omitempty"`
	Tags                []json.RawMessage          `json:"tags,omitempty"`
	Paths               map[string]json.RawMessage `json:"paths,omitempty"`
	Definitions         map[string]json.RawMessage `json:"definitions,omitempty"`
	SecurityDefinitions json.RawMessage            `json:"securityDefinitions,omitempty"`
}

// mergedSpec combines the registered fragments into one OpenAPI 2.0 document,
// rebasing each fragment's paths onto its registration's mount prefix.
func (r *Registrar) mergedSpec() ([]byte, error) {
	out := swaggerFragment{
		Swagger:     "2.0",
		Info:        r.docInfo,
		BasePath:    "/",
		Paths:       map[string]json.RawMessage{},
		Definitions: map[string]json.RawMessage{},
	}
	if out.Info == nil {
		out.Info = &DocInfo{Title: "API"}
	}

	seenTags := map[string]bool{}
	for _, e := range r.specs {
		var frag swaggerFragment
		if err := json.Unmarshal(e.fragment, &frag); err != nil {
			return nil, fmt.Errorf("failed to parse spec fragment for %q: %w", e.prefix, err)
		}

		for p, item := range frag.Paths {
			target := e.prefix + p
			for _, root := range rootAnchoredPaths {
				if strings.HasPrefix(p, root+"/") || p == root {
					target = p
					break
				}
			}
			if _, ok := out.Paths[target]; ok {
				slog.Warn("duplicate path in merged spec", "path", target)
				continue
			}
			out.Paths[target] = item
		}

		for name, def := range frag.Definitions {
			if existing, ok := out.Definitions[name]; ok {
				if !bytes.Equal(existing, def) {
					slog.Warn("conflicting definition in merged spec, keeping first", "definition", name)
				}
				continue
			}
			out.Definitions[name] = def
		}

		for _, tag := range frag.Tags {
			var t struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal(tag, &t); err != nil || t.Name == "" || seenTags[t.Name] {
				continue
			}
			seenTags[t.Name] = true
			out.Tags = append(out.Tags, tag)
		}

		if out.SecurityDefinitions == nil && frag.SecurityDefinitions != nil {
			out.SecurityDefinitions = frag.SecurityDefinitions
		}
	}

	return json.Marshal(out)
}

// serveDocs mounts the merged OpenAPI document and a Scalar reference page.
func (r *Registrar) serveDocs() {
	if len(r.specs) == 0 {
		return
	}
	r.specs = append(r.specs, specEntry{prefix: r.basePath, fragment: systemSpec})
	spec, err := r.mergedSpec()
	if err != nil {
		slog.Error("failed to merge spec fragments, docs not served", "error", err)
		return
	}

	specPath := r.basePath + "/api-spec/swagger.json"
	r.api.Get("/api-spec/swagger.json", func(c *fiber.Ctx) error {
		c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
		return c.Send(spec)
	})

	page := fmt.Sprintf(`<!doctype html>
<html>
<head>
  <title>API Reference</title>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
</head>
<body>
  <script id="api-reference" data-url="%s"></script>
  <script src="https://cdn.jsdelivr.net/npm/@scalar/api-reference"></script>
</body>
</html>`, specPath)
	r.api.Get("/docs", func(c *fiber.Ctx) error {
		c.Set(fiber.HeaderContentType, fiber.MIMETextHTMLCharsetUTF8)
		return c.SendString(page)
	})
}
