// internal/middleware/middleware_test.go
package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wake/tmux-box/internal/middleware"
)

var ok = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })

func TestIPWhitelistAllowed(t *testing.T) {
	h := middleware.IPWhitelist([]string{"192.168.1.0/24"})(ok)
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "192.168.1.50:12345"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Errorf("want 200, got %d", rec.Code)
	}
}

func TestIPWhitelistDenied(t *testing.T) {
	h := middleware.IPWhitelist([]string{"192.168.1.0/24"})(ok)
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Errorf("want 403, got %d", rec.Code)
	}
}

func TestIPWhitelistEmptyAllowsAll(t *testing.T) {
	h := middleware.IPWhitelist(nil)(ok)
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "1.2.3.4:12345"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Errorf("want 200, got %d", rec.Code)
	}
}

func TestTokenAuthValid(t *testing.T) {
	h := middleware.TokenAuth("secret")(ok)
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Errorf("want 200, got %d", rec.Code)
	}
}

func TestTokenAuthInvalid(t *testing.T) {
	h := middleware.TokenAuth("secret")(ok)
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Errorf("want 401, got %d", rec.Code)
	}
}

func TestTokenAuthCaseSensitive(t *testing.T) {
	h := middleware.TokenAuth("Secret")(ok)
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Errorf("want 401 (case mismatch), got %d", rec.Code)
	}
}

func TestTokenAuthQueryParam(t *testing.T) {
	h := middleware.TokenAuth("secret")(ok)
	req := httptest.NewRequest("GET", "/?token=secret", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Errorf("want 200 for valid query param token, got %d", rec.Code)
	}
}

func TestTokenAuthEmptyAllowsAll(t *testing.T) {
	h := middleware.TokenAuth("")(ok)
	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Errorf("want 200, got %d", rec.Code)
	}
}

func TestCORSHeaders(t *testing.T) {
	h := middleware.CORS(ok)
	req := httptest.NewRequest("OPTIONS", "/", nil)
	req.Header.Set("Origin", "http://example.com")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Error("want CORS Allow-Origin *")
	}
	if rec.Code != 204 {
		t.Errorf("want 204 for OPTIONS, got %d", rec.Code)
	}
}
