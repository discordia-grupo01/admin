package handler

import (
	"github.com/discordia-grupo01/admin/internal/apierr"
	"github.com/discordia-grupo01/admin/internal/logging"
	"github.com/discordia-grupo01/admin/internal/middleware"
	"github.com/gin-gonic/gin"
)

// RouteRegistrar agrega rutas al grupo /v1/admin, que ya está protegido por
// autenticación y por el rol de staff. Cada historia registra las suyas.
type RouteRegistrar func(admin *gin.RouterGroup)

type Deps struct {
	JWTSecret []byte
	Staff     middleware.StaffChecker
	Health    *HealthHandler
}

// NewRouter arma el router: /livez y /readyz públicos, y /v1/admin exigiendo
// un JWT válido de un usuario que esté en staff_members.
func NewRouter(deps Deps, registrars ...RouteRegistrar) *gin.Engine {
	r := gin.New()
	r.Use(logging.Middleware(), logging.Recovery())

	r.GET("/livez", deps.Health.Livez)
	r.GET("/readyz", deps.Health.Readyz)

	admin := r.Group("/v1/admin", middleware.RequireAuth(deps.JWTSecret), middleware.RequireStaff(deps.Staff))
	for _, register := range registrars {
		register(admin)
	}

	r.NoRoute(apierr.NotFound)
	return r
}
