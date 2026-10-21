package web

import (
	"bytes"
	"embed"
	"html/template"
	"io/fs"
	"net/http"
	"time"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

var tmpl = template.Must(template.New("").Funcs(templateFuncs).ParseFS(templateFS, "templates/*.html"))

// sampleInterval is how often the dashboard samples the broker.
const sampleInterval = time.Second

func RegisterWebHandlers() {
	monitor := NewMonitor()
	monitor.Tick(time.Now())
	go monitor.Run(sampleInterval)

	registerRoutes(http.DefaultServeMux, monitor)
}

func registerRoutes(mux *http.ServeMux, monitor *Monitor) {
	static, _ := fs.Sub(staticFS, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(static)))

	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		render(w, "home.html", newPage(monitor.Latest(), time.Now()))
	})

	mux.HandleFunc("GET /status-report", func(w http.ResponseWriter, r *http.Request) {
		render(w, "StatusReport", newPage(monitor.Latest(), time.Now()))
	})
}

func render(w http.ResponseWriter, name string, data page) {
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		http.Error(w, "Failed to render dashboard: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	buf.WriteTo(w)
}
