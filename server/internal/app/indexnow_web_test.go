package app

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"knowforge/server/internal/plugins"
	"knowforge/server/internal/plugins/indexnow"
)

func TestIndexNowRootVerificationFileIsServedAsPlainText(t *testing.T) {
	app, _, db := newContentImportTestApp(t)
	if err := app.setSetting("indexnow_enabled", "true", "test plugin setting"); err != nil {
		t.Fatal(err)
	}
	app.syncPluginState()
	key := "f581230d8e8ef0c5b36c5eb6c557c4dc"
	if err := db.Create(&indexnow.Config{ID: 1, Key: key}).Error; err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	RegisterWeb(router, nil, app.serveIndexNowVerificationFile)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/"+key+".txt", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("verification file status = %d, want 200; body=%q", response.Code, response.Body.String())
	}
	if got := response.Header().Get("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Fatalf("Content-Type = %q", got)
	}
	if got := response.Body.String(); got != key {
		t.Fatalf("verification body = %q, want %q", got, key)
	}
	if got := response.Header().Get("Cache-Control"); got != "public, no-cache, must-revalidate" {
		t.Fatalf("Cache-Control = %q", got)
	}

	wrong := httptest.NewRecorder()
	router.ServeHTTP(wrong, httptest.NewRequest(http.MethodGet, "/0123456789abcdef0123456789abcdef.txt", nil))
	if wrong.Code != http.StatusNotFound {
		t.Fatalf("wrong key status = %d, want 404", wrong.Code)
	}

	if err := app.setSetting("indexnow_enabled", "false", "test plugin setting"); err != nil {
		t.Fatal(err)
	}
	disabled := httptest.NewRecorder()
	router.ServeHTTP(disabled, httptest.NewRequest(http.MethodGet, "/"+key+".txt", nil))
	if disabled.Code != http.StatusNotFound {
		t.Fatalf("disabled plugin verification status = %d, want 404", disabled.Code)
	}
	if app.pluginEnabled(plugins.KeyIndexNow) {
		t.Fatal("IndexNow should be disabled for the verification request")
	}
}

func TestRegisterWebDoesNotInterceptOtherTextFiles(t *testing.T) {
	router := gin.New()
	RegisterWeb(router, nil, func(c *gin.Context) {
		t.Fatal("non-key .txt path should be proxied, not treated as an IndexNow verification file")
	})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/robots.txt", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("without embedded Web runtime /robots.txt should retain fallback status, got %d", response.Code)
	}
}
