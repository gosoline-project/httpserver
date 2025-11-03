package httpserver

import (
	"embed"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
)

func CreateEmbeddedHandler(files embed.FS, dir string, excludes ...string) (func(c *gin.Context), error) {
	var err error
	var dist fs.FS
	var file fs.File
	var stat fs.FileInfo

	if dist, err = fs.Sub(files, dir); err != nil {
		return nil, fmt.Errorf("failed to sub %q directory: %w", dir, err)
	}

	return func(c *gin.Context) {
		for _, exclude := range excludes {
			if strings.HasPrefix(c.Request.URL.Path, exclude) {
				return
			}
		}

		path := strings.TrimPrefix(c.Request.URL.Path, "/")
		ext := filepath.Ext(path)
		contentType := mime.TypeByExtension(ext)

		if path == "" || ext == "" {
			path = "index.html"
		}

		if file, err = dist.Open(path); err != nil {
			c.String(http.StatusNotFound, "failed to open %s: %w", path, err)
			c.Abort()

			return
		}

		if stat, err = file.Stat(); err != nil {
			c.String(http.StatusNotFound, "failed to get file size %s: %w", path, err)
			c.Abort()

			return
		}

		c.DataFromReader(http.StatusOK, stat.Size(), contentType, file, map[string]string{})
		c.Abort()
	}, nil
}
