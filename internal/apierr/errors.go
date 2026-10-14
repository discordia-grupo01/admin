// Package apierr define el formato de error común de la API
// ({"error": {"code", "message", "details"}}) acordado en el contrato previo.
package apierr

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

const (
	CodeSessionExpired = "SESSION_EXPIRED"
	CodeForbidden      = "FORBIDDEN"
	CodeNotFound       = "NOT_FOUND"
	CodeInternal       = "INTERNAL_ERROR"
	CodeInvalidQuery   = "INVALID_QUERY"
)

type Body struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details"`
}

type ErrorResponse struct {
	Error Body `json:"error"`
}

// Abort corta la request con el error indicado. details nil se serializa como {}.
func Abort(c *gin.Context, status int, code, message string, details map[string]any) {
	if details == nil {
		details = map[string]any{}
	}
	c.AbortWithStatusJSON(status, ErrorResponse{Error: Body{Code: code, Message: message, Details: details}})
}

func Unauthorized(c *gin.Context) {
	Abort(c, http.StatusUnauthorized, CodeSessionExpired, "La sesión expiró o ya no es válida", nil)
}

func Forbidden(c *gin.Context) {
	Abort(c, http.StatusForbidden, CodeForbidden, "No tenés permisos de staff para realizar esta acción", nil)
}

func NotFound(c *gin.Context) {
	Abort(c, http.StatusNotFound, CodeNotFound, "El recurso solicitado no existe", nil)
}

func Internal(c *gin.Context) {
	Abort(c, http.StatusInternalServerError, CodeInternal, "Error interno del servidor", nil)
}
