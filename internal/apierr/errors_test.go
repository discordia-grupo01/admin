package apierr

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func parse(query string) (value int, ok bool, w *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w = httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/x"+query, nil)
	value, ok = ParseNonNegativeIntQuery(c, "limit", 20)
	return value, ok, w
}

func TestParseNonNegativeIntQuery(t *testing.T) {
	cases := []struct {
		query string
		want  int
	}{
		{"", 20},
		{"?limit=0", 0},
		{"?limit=5", 5},
		{"?limit=500", 500}, // acotar al máximo es decisión de quien la usa
	}
	for _, tc := range cases {
		got, ok, _ := parse(tc.query)
		if !ok || got != tc.want {
			t.Errorf("%q: got=%d ok=%v, quería %d", tc.query, got, ok, tc.want)
		}
	}
}

func TestParseNonNegativeIntQueryRejectsInvalid(t *testing.T) {
	for _, query := range []string{"?limit=-1", "?limit=abc", "?limit=1.5", "?limit=%20", "?limit=", "?limit=9007199254740992"} {
		_, ok, w := parse(query)
		if ok {
			t.Errorf("%q debería ser inválido", query)
			continue
		}
		var body ErrorResponse
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("%q: el error no sigue el formato: %v", query, err)
		}
		if w.Code != http.StatusBadRequest || body.Error.Code != CodeInvalidQuery || body.Error.Details["param"] != "limit" {
			t.Errorf("%q: status=%d body=%+v", query, w.Code, body)
		}
	}
}

// assertEnvelope verifica la forma completa del sobre de error (RF1) y devuelve el code.
func assertEnvelope(t *testing.T, body []byte) string {
	t.Helper()
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil || len(raw) != 1 || raw["error"] == nil {
		t.Fatalf("el body no es el sobre de error: %v (%s)", err, body)
	}
	var e struct {
		Code    string         `json:"code"`
		Message string         `json:"message"`
		Details map[string]any `json:"details"`
	}
	if err := json.Unmarshal(raw["error"], &e); err != nil {
		t.Fatalf("error mal formado: %v (%s)", err, body)
	}
	if !regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`).MatchString(e.Code) || e.Message == "" || e.Details == nil {
		t.Fatalf("sobre inválido: %s", body)
	}
	return e.Code
}

func TestAbortNormalizesNilDetails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/x", nil)
	Abort(c, http.StatusConflict, "ALGO_PASO", "Pasó algo", nil)
	if w.Code != http.StatusConflict || assertEnvelope(t, w.Body.Bytes()) != "ALGO_PASO" {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"details":{}`) {
		t.Errorf("details nulo debe serializarse como {}: %s", w.Body.String())
	}
}

func TestWrappersUseCatalogCodes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		name   string
		call   func(*gin.Context)
		status int
		code   string
	}{
		{"unauthorized", Unauthorized, 401, "UNAUTHORIZED"},
		{"session expired", SessionExpired, 401, "SESSION_EXPIRED"},
		{"forbidden", Forbidden, 403, "FORBIDDEN"},
		{"not found", NotFound, 404, "NOT_FOUND"},
		{"method not allowed", MethodNotAllowed, 405, "METHOD_NOT_ALLOWED"},
		{"internal", Internal, 500, "INTERNAL_ERROR"},
		{"invalid body", func(c *gin.Context) { InvalidBody(c, "mal", "", "") }, 400, "INVALID_BODY"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodGet, "/x", nil)
			tc.call(c)
			if w.Code != tc.status || assertEnvelope(t, w.Body.Bytes()) != tc.code {
				t.Fatalf("status %d body %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestInvalidBodyDetailsField(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/x", nil)
	InvalidBody(c, "Campo inválido", "confirm_name", "required")
	var body ErrorResponse
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body.Error.Details["field"] != "confirm_name" || body.Error.Details["reason"] != "required" {
		t.Errorf("details = %v", body.Error.Details)
	}
}
