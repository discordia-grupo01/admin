package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/discordia-grupo01/admin/internal/apierr"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

const (
	secret  = "test-secret"
	staffID = "11111111-1111-1111-1111-111111111111"
	userID  = "22222222-2222-2222-2222-222222222222"
)

type fakeStaff struct{ err error }

func (f fakeStaff) IsStaff(_ context.Context, id string) (bool, error) {
	return id == staffID, f.err
}

func bearer(t *testing.T, sub string) string {
	t.Helper()
	s, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": sub, "exp": time.Now().Add(time.Hour).Unix(),
	}).SignedString([]byte(secret))
	if err != nil {
		t.Fatal(err)
	}
	return "Bearer " + s
}

func okCheck(name string) Check {
	return Check{Name: name, Fn: func(context.Context) error { return nil }}
}

func newTestRouter(health *HealthHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	return NewRouter(Deps{JWTSecret: []byte(secret), Staff: fakeStaff{}, Health: health},
		func(admin *gin.RouterGroup) {
			admin.GET("/ping", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"pong": true}) })
		})
}

func get(r http.Handler, path, auth string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestLivezNeverTouchesDependencies(t *testing.T) {
	called := false
	failing := Check{Name: "db", Fn: func(context.Context) error { called = true; return errors.New("caída") }}
	w := get(newTestRouter(NewHealthHandler(failing)), "/livez", "")

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if called {
		t.Error("/livez no debe consultar dependencias")
	}
}

func TestReadyzOK(t *testing.T) {
	w := get(newTestRouter(NewHealthHandler(okCheck("db"), okCheck("rabbitmq"))), "/readyz", "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "ready") {
		t.Fatalf("status = %d body %s", w.Code, w.Body.String())
	}
}

func TestReadyzReportsFailedDependenciesWithoutLeakingErrors(t *testing.T) {
	failing := Check{Name: "postgres", Fn: func(context.Context) error {
		return errors.New("password authentication failed for user discordia")
	}}
	w := get(newTestRouter(NewHealthHandler(okCheck("rabbitmq"), failing)), "/readyz", "")

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", w.Code)
	}
	var body struct {
		Status string   `json:"status"`
		Failed []string `json:"failed"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Status != "unavailable" || len(body.Failed) != 1 || body.Failed[0] != "postgres" {
		t.Errorf("body = %+v", body)
	}
	if strings.Contains(w.Body.String(), "password") {
		t.Error("/readyz no debe filtrar el detalle del error")
	}
}

func TestReadyzWithoutChecksIsReady(t *testing.T) {
	if w := get(newTestRouter(NewHealthHandler()), "/readyz", ""); w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestAdminGroupRequiresAuthAndStaff(t *testing.T) {
	r := newTestRouter(NewHealthHandler())

	cases := []struct {
		name   string
		auth   string
		status int
		code   string
	}{
		{"sin token", "", http.StatusUnauthorized, apierr.CodeSessionExpired},
		{"usuario que no es staff", bearer(t, userID), http.StatusForbidden, apierr.CodeForbidden},
		{"staff", bearer(t, staffID), http.StatusOK, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := get(r, "/v1/admin/ping", tc.auth)
			if w.Code != tc.status {
				t.Fatalf("status = %d, quería %d (%s)", w.Code, tc.status, w.Body.String())
			}
			if tc.code != "" {
				var body apierr.ErrorResponse
				_ = json.Unmarshal(w.Body.Bytes(), &body)
				if body.Error.Code != tc.code {
					t.Errorf("code = %q, quería %q", body.Error.Code, tc.code)
				}
			}
		})
	}
}

func TestUnknownRouteUsesErrorFormat(t *testing.T) {
	w := get(newTestRouter(NewHealthHandler()), "/v1/no-existe", "")
	var body apierr.ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("el 404 debe seguir el formato de error: %v (%s)", err, w.Body.String())
	}
	if w.Code != http.StatusNotFound || body.Error.Code != apierr.CodeNotFound {
		t.Fatalf("status = %d body %+v", w.Code, body)
	}
}
