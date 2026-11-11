package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

type recordedChanges struct{ calls [][]string }

func (r *recordedChanges) Changed(_ context.Context, resources ...string) {
	r.calls = append(r.calls, resources)
}

func changesRouter(n *recordedChanges, status int) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	admin := r.Group("/v1/admin", NotifyChanges(n))
	reply := func(c *gin.Context) { c.Status(status) }
	admin.POST("/users/:id/suspension", reply)
	admin.DELETE("/users/:id/suspension", reply)
	admin.DELETE("/servers/:id", reply)
	admin.POST("/servers/:id/suspension", reply)
	admin.DELETE("/servers/:id/suspension", reply)
	admin.GET("/users", reply)
	return r
}

func callRoute(r *gin.Engine, method, path string) {
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(method, path, nil))
}

func TestNotifyChangesTellsWhatEachStaffActionChanged(t *testing.T) {
	cases := []struct {
		method, path string
		want         []string
	}{
		{http.MethodPost, "/v1/admin/users/u1/suspension", []string{"users", "dossier"}},
		{http.MethodDelete, "/v1/admin/users/u1/suspension", []string{"users", "dossier"}},
		{http.MethodDelete, "/v1/admin/servers/s1", []string{"servers", "dossier"}},
		{http.MethodPost, "/v1/admin/servers/s1/suspension", []string{"servers", "dossier"}},
		{http.MethodDelete, "/v1/admin/servers/s1/suspension", []string{"servers", "dossier"}},
	}
	for _, tc := range cases {
		n := &recordedChanges{}
		callRoute(changesRouter(n, http.StatusOK), tc.method, tc.path)
		if len(n.calls) != 1 || len(n.calls[0]) != len(tc.want) || n.calls[0][0] != tc.want[0] || n.calls[0][1] != tc.want[1] {
			t.Errorf("%s %s: told %v, want %v", tc.method, tc.path, n.calls, tc.want)
		}
	}
}

func TestNotifyChangesStaysQuietWhenTheActionFailedOrWasARead(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusNotFound, http.StatusConflict, http.StatusInternalServerError} {
		n := &recordedChanges{}
		callRoute(changesRouter(n, status), http.MethodPost, "/v1/admin/users/u1/suspension")
		if len(n.calls) != 0 {
			t.Errorf("status %d told %v", status, n.calls)
		}
	}

	n := &recordedChanges{}
	callRoute(changesRouter(n, http.StatusOK), http.MethodGet, "/v1/admin/users")
	if len(n.calls) != 0 {
		t.Errorf("a read told %v", n.calls)
	}
}
