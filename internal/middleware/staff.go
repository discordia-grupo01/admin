package middleware

import (
	"context"
	"log"

	"github.com/discordia-grupo01/admin/internal/apierr"
	"github.com/gin-gonic/gin"
)

// StaffChecker dice si un usuario pertenece al staff de la plataforma.
type StaffChecker interface {
	IsStaff(ctx context.Context, userID string) (bool, error)
}

// RequireStaff exige que el usuario autenticado (RequireAuth tiene que correr
// antes) esté en staff_members. Un usuario que no es staff recibe 403; un
// error de base recibe 500 (nunca se asume staff ante la duda).
func RequireStaff(checker StaffChecker) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := UserID(c)
		if userID == "" {
			apierr.Unauthorized(c)
			return
		}
		ok, err := checker.IsStaff(c.Request.Context(), userID)
		if err != nil {
			log.Printf("middleware: no se pudo verificar staff user_id=%s: %v", userID, err)
			apierr.Internal(c)
			return
		}
		if !ok {
			apierr.Forbidden(c)
			return
		}
		c.Next()
	}
}
