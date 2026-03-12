package server

import (
	"net/http"
	"strings"
)

// photosHandler serves static photo files from root, stripping the /photos
// prefix. Directory listings are disabled (404 on directories).
func photosHandler(root http.FileSystem) http.HandlerFunc {
	fs := http.StripPrefix("/photos", http.FileServer(root))

	return func(w http.ResponseWriter, r *http.Request) {
		// Block directory listings: reject bare "/photos" and trailing-slash paths.
		if strings.HasSuffix(r.URL.Path, "/") || r.URL.Path == "/photos" {
			http.NotFound(w, r)
			return
		}

		w.Header().Set("Cache-Control", "public, max-age=3600")
		fs.ServeHTTP(w, r)
	}
}
