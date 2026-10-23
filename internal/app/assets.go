package app

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"net/http"
	"path"
)

//go:embed templates/*.html static/*
var pageFiles embed.FS

// staticVersions maps each embedded static file to a short hash of its
// contents. Page links carry the hash as a query string, so a deploy that
// changes a file also changes its URL and browsers fetch the new copy.
var staticVersions = func() map[string]string {
	versions := map[string]string{}
	_ = fs.WalkDir(pageFiles, "static", func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		contents, err := pageFiles.ReadFile(name)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(contents)
		versions[name[len("static/"):]] = hex.EncodeToString(sum[:6])
		return nil
	})
	return versions
}()

// staticURL links to an embedded static file relative to fromPath, with its
// content version.
func staticURL(fromPath, name string) string {
	target := relativeURL(fromPath, path.Join("/static", name))
	if version := staticVersions[name]; version != "" {
		target += "?v=" + version
	}
	return target
}

// staticHandler serves the embedded static files. A request whose version
// matches the file's current content is cached for a year without
// revalidation; any other request, including an unversioned or stale link,
// revalidates against the content version as its ETag.
func staticHandler(files fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(files))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if version := staticVersions[r.URL.Path]; version != "" {
			if r.URL.Query().Get("v") == version {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			} else {
				w.Header().Set("Cache-Control", "no-cache")
			}
			w.Header().Set("ETag", `"`+version+`"`)
		}
		fileServer.ServeHTTP(w, r)
	})
}
