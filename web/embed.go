// Package web embeds the built Vue app (npm run build writes it to
// dist/app). Without a build the app is empty and the UI serves only its API.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// App returns the built app.
func App() fs.FS {
	app, err := fs.Sub(dist, "dist/app")
	if err != nil {
		panic(err)
	}
	return app
}
