package bsv21

import (
	_ "embed"
	"strings"

	"github.com/gofiber/fiber/v2"
)

//go:embed ui/index.html
var indexHTML []byte

// registerBrowser mounts the token browser under /browse so it does not
// collide with the JSON API. Unknown paths return the app shell.
func registerBrowser(router fiber.Router) {
	router.Get("/", func(c *fiber.Ctx) error {
		path, query, _ := strings.Cut(c.OriginalURL(), "?")
		if !strings.HasSuffix(path, "/") {
			path += "/"
		}
		dest := path + "browse/"
		if query != "" {
			dest += "?" + query
		}
		return c.Redirect(dest, fiber.StatusFound)
	})
	router.Get("/browse", serveBrowser)
	router.Get("/browse/", serveBrowser)
	router.Get("/browse/*", serveBrowser)
}

func serveBrowser(c *fiber.Ctx) error {
	raw := c.OriginalURL()
	path, query, _ := strings.Cut(raw, "?")
	if strings.HasSuffix(path, "/browse") {
		dest := path + "/"
		if query != "" {
			dest += "?" + query
		}
		return c.Redirect(dest, fiber.StatusMovedPermanently)
	}
	c.Set("Content-Type", "text/html; charset=utf-8")
	return c.Send(indexHTML)
}
