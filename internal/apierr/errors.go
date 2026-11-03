// Package apierr define el formato de error común de la API
// ({"error": {"code", "message", "details"}}) acordado en el contrato previo.
package apierr

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

const (
	CodeSessionExpired       = "SESSION_EXPIRED"
	CodeForbidden            = "FORBIDDEN"
	CodeNotFound             = "NOT_FOUND"
	CodeInternal             = "INTERNAL_ERROR"
	CodeInvalidQuery         = "INVALID_QUERY"
	CodeUserNotFound         = "USER_NOT_FOUND"
	CodeCannotSuspendStaff   = "CANNOT_SUSPEND_STAFF"
	CodeUserAlreadySuspended = "USER_ALREADY_SUSPENDED"
	CodeUserNotSuspended     = "USER_NOT_SUSPENDED"

	CodeInvalidBody                = "INVALID_BODY"
	CodeServerNotFound             = "SERVER_NOT_FOUND"
	CodeDeleteConfirmationMismatch = "DELETE_CONFIRMATION_MISMATCH"
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

// InvalidQuery responde 400 INVALID_QUERY indicando qué parámetro falló.
func InvalidQuery(c *gin.Context, param, message string) {
	Abort(c, http.StatusBadRequest, CodeInvalidQuery, message, map[string]any{"param": param})
}

// ParseNonNegativeIntQuery lee un parámetro entero >= 0 de la query string.
// Si falta devuelve def; si es inválido responde 400 y devuelve ok=false.
func ParseNonNegativeIntQuery(c *gin.Context, key string, def int) (value int, ok bool) {
	raw := c.Query(key)
	if raw == "" {
		return def, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		InvalidQuery(c, key, "El parámetro "+key+" debe ser un entero mayor o igual a 0")
		return 0, false
	}
	return n, true
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
