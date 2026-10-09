package app

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"knowforge/server/internal/plugins/indexnow"
)

func (a *App) serveIndexNowVerificationFile(c *gin.Context) {
	candidate, _ := c.Get("indexnow_verification_key")
	key, ok := candidate.(string)
	if !ok || !indexnow.VerificationKeyMatches(a, key) {
		c.Status(http.StatusNotFound)
		return
	}
	c.Header("Cache-Control", "public, no-cache, must-revalidate")
	c.Data(http.StatusOK, "text/plain; charset=utf-8", []byte(key))
}
