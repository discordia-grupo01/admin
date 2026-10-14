package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/discordia-grupo01/admin/internal/apierr"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

const (
	testSecret = "test-secret"
	staffID    = "11111111-1111-1111-1111-111111111111"
	otherID    = "22222222-2222-2222-2222-222222222222"
)

type fakeStaff struct {
	staff map[string]bool
	err   error
}

func (f fakeStaff) IsStaff(_ context.Context, id string) (bool, error) {
	return f.staff[id], f.err
}

func token(t *testing.T, method jwt.SigningMethod, secret string, claims jwt.MapClaims) string {
	t.Helper()
	s, err := jwt.NewWithClaims(method, claims).SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("firmar: %v", err)
	}
	return s
}

func validClaims(sub string) jwt.MapClaims {
	return jwt.MapClaims{"sub": sub, "exp": time.Now().Add(time.Hour).Unix()}
}

func newRouter(checker StaffChecker) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/secure", RequireAuth([]byte(testSecret)), RequireStaff(checker), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"user_id": UserID(c)})
	})
	return r
}

func do(r *gin.Engine, authHeader string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/secure", nil)
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func errorCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var body apierr.ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("body no es un ErrorResponse: %v (%s)", err, w.Body.String())
	}
	if body.Error.Details == nil {
		t.Error("details debería ser {} y no null")
	}
	return body.Error.Code
}

func TestStaffAllowed(t *testing.T) {
	r := newRouter(fakeStaff{staff: map[string]bool{staffID: true}})
	w := do(r, "Bearer "+token(t, jwt.SigningMethodHS256, testSecret, validClaims(staffID)))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", w.Code, w.Body.String())
	}
	var body map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["user_id"] != staffID {
		t.Errorf("user_id = %q", body["user_id"])
	}
}

func TestNotStaffForbidden(t *testing.T) {
	r := newRouter(fakeStaff{staff: map[string]bool{staffID: true}})
	w := do(r, "Bearer "+token(t, jwt.SigningMethodHS256, testSecret, validClaims(otherID)))
	if w.Code != http.StatusForbidden || errorCode(t, w) != apierr.CodeForbidden {
		t.Fatalf("status = %d, body %s", w.Code, w.Body.String())
	}
}

func TestStaffCheckerErrorIsInternal(t *testing.T) {
	r := newRouter(fakeStaff{err: errors.New("db caída")})
	w := do(r, "Bearer "+token(t, jwt.SigningMethodHS256, testSecret, validClaims(staffID)))
	if w.Code != http.StatusInternalServerError || errorCode(t, w) != apierr.CodeInternal {
		t.Fatalf("ante un error de base nunca debe asumir staff: status = %d, body %s", w.Code, w.Body.String())
	}
}

func TestUnauthorizedCases(t *testing.T) {
	r := newRouter(fakeStaff{staff: map[string]bool{staffID: true}})

	expired := validClaims(staffID)
	expired["exp"] = time.Now().Add(-time.Minute).Unix()
	noExp := jwt.MapClaims{"sub": staffID}
	noSub := jwt.MapClaims{"exp": time.Now().Add(time.Hour).Unix()}

	cases := map[string]string{
		"sin header":             "",
		"sin Bearer":             "Token abc",
		"token basura":           "Bearer no.es.jwt",
		"firma de otra clave":    "Bearer " + token(t, jwt.SigningMethodHS256, "otra-clave", validClaims(staffID)),
		"vencido":                "Bearer " + token(t, jwt.SigningMethodHS256, testSecret, expired),
		"sin exp":                "Bearer " + token(t, jwt.SigningMethodHS256, testSecret, noExp),
		"sin sub":                "Bearer " + token(t, jwt.SigningMethodHS256, testSecret, noSub),
		"sub que no es UUID":     "Bearer " + token(t, jwt.SigningMethodHS256, testSecret, validClaims("hola")),
		"algoritmo no permitido": "Bearer " + token(t, jwt.SigningMethodHS512, testSecret, validClaims(staffID)),
	}
	for name, header := range cases {
		t.Run(name, func(t *testing.T) {
			w := do(r, header)
			if w.Code != http.StatusUnauthorized || errorCode(t, w) != apierr.CodeSessionExpired {
				t.Fatalf("status = %d, body %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestRequireStaffWithoutAuthIsUnauthorized(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/x", RequireStaff(fakeStaff{}), func(c *gin.Context) { c.Status(http.StatusOK) })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", w.Code)
	}
}
