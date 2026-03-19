package landing

import (
	"embed"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/filesystem"
)

//go:embed ui/dist/*
var uiFS embed.FS

type Routes struct {
	config *RoutesConfig
	logger *slog.Logger
}

func NewRoutes(cfg *RoutesConfig, logger *slog.Logger) *Routes {
	return &Routes{config: cfg, logger: logger}
}

func (r *Routes) Register(group fiber.Router) {
	uiSubFS, err := fs.Sub(uiFS, "ui/dist")
	if err != nil {
		r.logger.Error("failed to create landing ui sub filesystem", "error", err)
		return
	}

	group.Get("/", func(c *fiber.Ctx) error {
		if !strings.HasSuffix(c.OriginalURL(), "/") {
			return c.Redirect(c.OriginalURL()+"/", fiber.StatusMovedPermanently)
		}
		content, err := fs.ReadFile(uiSubFS, "index.html")
		if err != nil {
			return c.Status(fiber.StatusNotFound).SendString("Not found")
		}
		c.Set("Content-Type", "text/html")
		return c.Send(content)
	})

	group.Use("/", filesystem.New(filesystem.Config{
		Root:   http.FS(uiSubFS),
		Browse: false,
	}))

	r.logger.Debug("registered landing routes")
}
