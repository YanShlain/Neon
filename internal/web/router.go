package web

import (
	"io/fs"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// Register mounts MVP-A static UI routes on the Gin engine.
func Register(r *gin.Engine, staticFS fs.FS) {
	// --- Page routes ---
	r.GET("/", servePage(staticFS, "index.html"))
	r.GET("/seats", servePage(staticFS, "seats.html"))
	r.GET("/payment", servePage(staticFS, "payment.html"))

	// --- Asset routes ---
	r.GET("/css/*filepath", serveAsset(staticFS, "css"))
	r.GET("/js/*filepath", serveAsset(staticFS, "js"))
}

func servePage(staticFS fs.FS, name string) gin.HandlerFunc {
	return func(c *gin.Context) {
		data, err := fs.ReadFile(staticFS, name)
		if err != nil {
			c.String(http.StatusNotFound, "not found")
			return
		}
		c.Data(http.StatusOK, ContentType(name), data)
	}
}

func serveAsset(staticFS fs.FS, prefix string) gin.HandlerFunc {
	return func(c *gin.Context) {
		path := prefix + c.Param("filepath")
		if strings.Contains(path, "..") {
			c.String(http.StatusBadRequest, "invalid path")
			return
		}
		data, err := fs.ReadFile(staticFS, path)
		if err != nil {
			c.String(http.StatusNotFound, "not found")
			return
		}
		c.Data(http.StatusOK, ContentType(path), data)
	}
}
