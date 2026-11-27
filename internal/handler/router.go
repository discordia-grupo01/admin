package handler

import (
	"net/http"

	"github.com/discordia-grupo01/admin/internal/apierr"
	"github.com/discordia-grupo01/admin/internal/changes"
	"github.com/discordia-grupo01/admin/internal/logging"
	"github.com/discordia-grupo01/admin/internal/middleware"
	"github.com/discordia-grupo01/admin/internal/telemetry"
	"github.com/gin-gonic/gin"
)

// RouteRegistrar agrega rutas al grupo /v1/admin, que ya está protegido por
// autenticación y por el rol de staff. Cada historia registra las suyas.
type RouteRegistrar func(admin *gin.RouterGroup)

type Deps struct {
	Changes   changes.Notifier
	JWTSecret []byte
	Staff     middleware.StaffChecker
	Health    *HealthHandler
}

// NewRouter arma el router: /livez y /readyz públicos, y /v1/admin exigiendo
// un JWT válido de un usuario que esté en staff_members.
func NewRouter(deps Deps, registrars ...RouteRegistrar) *gin.Engine {
	r := gin.New()
	r.Use(telemetry.Middleware(), logging.Middleware(), logging.Recovery())

	r.GET("/livez", deps.Health.Livez)
	r.GET("/readyz", deps.Health.Readyz)

	admin := r.Group("/v1/admin", middleware.RequireAuth(deps.JWTSecret), middleware.RequireStaff(deps.Staff))
	if deps.Changes != nil {
		admin.Use(middleware.NotifyChanges(deps.Changes))
	}
	admin.GET("/me", staffSession)
	for _, register := range registrars {
		register(admin)
	}

	r.HandleMethodNotAllowed = true
	r.RedirectTrailingSlash = false
	r.RedirectFixedPath = false
	r.NoMethod(apierr.MethodNotAllowed)
	r.NoRoute(apierr.NotFound)
	return r
}

// staffSession atiende GET /v1/admin/me: 204 si quien llama es staff. Los
// middlewares del grupo ya responden 401 o 403 en cualquier otro caso.
//
// @Summary      Verificar que quien llama es staff
// @Description  El backoffice lo llama al iniciar sesión, después de validar las credenciales con identify, para no dejar entrar a quien no es staff.
// @Tags         Session
// @Security     BearerAuth
// @Success      204  "Quien llama es staff de la plataforma"
// @Failure      401  {object}  apierr.ErrorResponse  "Falta el token o no es válido (`UNAUTHORIZED`), o la sesión venció (`SESSION_EXPIRED`)"
// @Failure      403  {object}  apierr.ErrorResponse  "El usuario no es staff de la plataforma (`FORBIDDEN`)"
// @Failure      500  {object}  apierr.ErrorResponse  "Error interno (`INTERNAL_ERROR`)"
// @Router       /v1/admin/me [get]
func staffSession(c *gin.Context) {
	c.Status(http.StatusNoContent)
}
