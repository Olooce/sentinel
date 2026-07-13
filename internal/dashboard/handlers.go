package dashboard

import (
	"embed"
	"html/template"
	"io/fs"
	"net/http"

	"github.com/gin-gonic/gin"

	"sentinel/internal/config"
	"sentinel/internal/events"
	"sentinel/internal/store"
)

//go:embed web/templates/index.html
var templateFS embed.FS

//go:embed web/static/*
var staticFS embed.FS

type Handler struct {
	store  *store.Store
	mgr    *config.Manager
	broker *events.Broker
	tmpl   *template.Template
}

func NewHandler(st *store.Store, mgr *config.Manager, broker *events.Broker) *Handler {
	tmpl := template.Must(template.ParseFS(templateFS, "web/templates/index.html"))
	return &Handler{
		store:  st,
		mgr:    mgr,
		broker: broker,
		tmpl:   tmpl,
	}
}

func (h *Handler) RegisterRoutes(r *gin.Engine) {
	sub, err := fs.Sub(staticFS, "web/static")
	if err != nil {
		panic(err)
	}

	r.StaticFS("/static", http.FS(sub))
	r.GET("/", h.serveDashboard)
}

func (h *Handler) serveDashboard(c *gin.Context) {
	data := gin.H{
		"Services": h.mgr.GetServices(),
		"Statuses": h.store.List(),
	}
	c.Status(http.StatusOK)
	c.Header("Content-Type", "text/html; charset=utf-8")
	if err := h.tmpl.Execute(c.Writer, data); err != nil {
		_ = c.Error(err)
	}
}
