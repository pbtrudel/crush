package web

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var distFS embed.FS

func embeddedAssets() fs.FS {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}

// assetHandler serves the frontend. Hashed files under /assets are
// immutable; everything else, including index.html, is revalidated.
// Directories answer 404 instead of the file server's listing.
func assetHandler(assets fs.FS) http.Handler {
	files := http.FileServerFS(assets)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			info, err := fs.Stat(assets, strings.TrimPrefix(path.Clean(r.URL.Path), "/"))
			switch {
			case err != nil && !strings.HasPrefix(r.URL.Path, "/assets/"):
				// Unknown paths render the app, which routes them.
				r.URL.Path = "/"
			case err == nil && info.IsDir():
				http.NotFound(w, r)
				return
			}
		}
		if strings.HasPrefix(r.URL.Path, "/assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		files.ServeHTTP(w, r)
	})
}
