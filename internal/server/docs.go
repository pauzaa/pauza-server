package server

import (
	"net/http"

	"github.com/IsorilovA/pauza-server/docs"
)

const docsHTML = `<!DOCTYPE html>
<html>
<head>
	<title>Pauza API Reference</title>
	<meta charset="utf-8" />
	<meta name="viewport" content="width=device-width, initial-scale=1" />
</head>
<body>
	<script id="api-reference" data-url="/docs/openapi.yaml"></script>
	<script src="https://cdn.jsdelivr.net/npm/@scalar/api-reference"></script>
</body>
</html>`

func docsPageHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(docsHTML))
	}
}

func docsSpecHandler() http.HandlerFunc {
	spec := docs.OpenAPISpec
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/yaml")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		_, _ = w.Write(spec)
	}
}
