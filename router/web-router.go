package router

import (
	"embed"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/gin-contrib/gzip"
	"github.com/gin-contrib/static"
	"github.com/gin-gonic/gin"
)

var docsContentTypes = map[string]string{
	".css":  "text/css; charset=utf-8",
	".gif":  "image/gif",
	".html": "text/html; charset=utf-8",
	".jpeg": "image/jpeg",
	".jpg":  "image/jpeg",
	".js":   "text/javascript; charset=utf-8",
	".png":  "image/png",
	".svg":  "image/svg+xml",
	".webp": "image/webp",
}

// WebAssets holds the embedded dashboard frontend assets.
type WebAssets struct {
	BuildFS   embed.FS
	IndexPage []byte
	DocsFS    embed.FS
}

func SetWebRouter(router *gin.Engine, assets WebAssets, pluginDispatcher gin.HandlerFunc) {
	frontendFS := common.EmbedFolder(assets.BuildFS, "web/dist")

	docsFS, err := fs.Sub(assets.DocsFS, "doc")
	if err != nil {
		panic(err)
	}
	docsRouter := router.Group("/docs", middleware.RouteTag("web"), gzip.Gzip(gzip.DefaultCompression), middleware.GlobalWebRateLimit())
	docsRouter.GET("", func(c *gin.Context) { c.Redirect(http.StatusMovedPermanently, "/docs/") })
	docsRouter.GET("/*filepath", serveDocs(docsFS))

	router.NoRoute(
		pluginDispatcher,
		middleware.RouteTag("web"),
		gzip.Gzip(gzip.DefaultCompression),
		middleware.AccessTokenAudit(),
		middleware.GlobalWebRateLimit(),
		middleware.Cache(),
		static.Serve("/", frontendFS),
		func(c *gin.Context) {
			if strings.HasPrefix(c.Request.RequestURI, "/v1") || strings.HasPrefix(c.Request.RequestURI, "/api") || strings.HasPrefix(c.Request.RequestURI, "/assets") {
				controller.RelayNotFound(c)
				return
			}
			c.Header("Cache-Control", "no-cache")
			c.Data(http.StatusOK, "text/html; charset=utf-8", assets.IndexPage)
		},
	)
}

func serveDocs(docsFS fs.FS) gin.HandlerFunc {
	return func(c *gin.Context) {
		requestPath := strings.TrimPrefix(c.Param("filepath"), "/")
		if requestPath == "" {
			requestPath = "index.html"
		}
		requestPath = path.Clean("/" + requestPath)
		requestPath = strings.TrimPrefix(requestPath, "/")
		if strings.HasSuffix(c.Param("filepath"), "/") {
			requestPath = path.Join(requestPath, "index.html")
		}

		if !docsFileExists(docsFS, requestPath) {
			if isDocsStaticAsset(requestPath) {
				controller.RelayNotFound(c)
				return
			}
			requestPath = "index.html"
		}

		if strings.HasSuffix(requestPath, ".html") {
			c.Header("Cache-Control", "no-cache, no-store, must-revalidate")
			c.Header("Pragma", "no-cache")
			c.Header("Expires", "0")
		} else {
			c.Header("Cache-Control", "no-cache, no-store, must-revalidate")
		}

		content, err := fs.ReadFile(docsFS, requestPath)
		if err != nil {
			controller.RelayNotFound(c)
			return
		}
		contentType := docsContentType(requestPath)
		c.Data(http.StatusOK, contentType, content)
	}
}

func docsFileExists(docsFS fs.FS, name string) bool {
	info, err := fs.Stat(docsFS, name)
	return err == nil && !info.IsDir()
}

func docsContentType(name string) string {
	ext := strings.ToLower(path.Ext(name))
	if contentType, ok := docsContentTypes[ext]; ok {
		return contentType
	}
	contentType := mime.TypeByExtension(ext)
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	return contentType
}

func isDocsStaticAsset(name string) bool {
	ext := strings.ToLower(path.Ext(name))
	if ext == "" || ext == ".html" {
		return false
	}
	return true
}
