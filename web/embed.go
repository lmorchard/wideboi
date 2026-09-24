package web

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed dist
var dist embed.FS

// DistFS returns a filesystem rooted at dist/
func DistFS() (http.FileSystem, error) {
	fSys, err := fs.Sub(dist, "dist")
	if err != nil {
		return nil, err
	}
	return http.FS(fSys), nil
}
