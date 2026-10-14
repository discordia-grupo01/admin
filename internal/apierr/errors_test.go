package apierr

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	for _, query := range []string{"?limit=-1", "?limit=abc", "?limit=1.5", "?limit=%20"} {
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
