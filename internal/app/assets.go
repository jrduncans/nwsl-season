package app

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
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
