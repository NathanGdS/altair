package web

import (
	"html/template"
	"net/http"
)

var tmpl *template.Template

func init() {
	tmpl, _ = template.ParseGlob("web/templates/*.html")
}

func RegisterWebHandlers() {

	http.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		err := tmpl.ExecuteTemplate(w, "home.html", nil)

		if err != nil {
			http.Error(w, "Erro ao renderizar o template: "+err.Error(), http.StatusInternalServerError)
			return
		}
	})
}
