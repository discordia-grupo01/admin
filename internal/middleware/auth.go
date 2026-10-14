// Package middleware tiene la autenticación (JWT) y la autorización (staff)
// de los endpoints /v1/admin.
package middleware

import (
	"strings"

	"github.com/discordia-grupo01/admin/internal/apierr"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const UserIDKey = "auth_user_id"

// UserID devuelve el id del usuario autenticado que dejó RequireAuth.
func UserID(c *gin.Context) string {
	return c.GetString(UserIDKey)
}

// RequireAuth valida el header Authorization: Bearer <JWT> firmado con HS256
// (el mismo JWT_SECRET que usa identify). Exige exp, y que sub sea un UUID.
// Kong ya valida la firma y exp; admin lo repite para no depender solo del
// gateway.
func RequireAuth(secret []byte) gin.HandlerFunc {
	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithExpirationRequired(),
	)
	return func(c *gin.Context) {
		authorization := c.GetHeader("Authorization")
		if !strings.HasPrefix(authorization, "Bearer ") {
			apierr.Unauthorized(c)
			return
		}

		claims := jwt.MapClaims{}
		token, err := parser.ParseWithClaims(strings.TrimPrefix(authorization, "Bearer "), claims, func(*jwt.Token) (any, error) {
			return secret, nil
		})
		if err != nil || !token.Valid {
			apierr.Unauthorized(c)
			return
		}

		sub, _ := claims["sub"].(string)
		if _, err := uuid.Parse(sub); err != nil {
			apierr.Unauthorized(c)
			return
		}

		c.Set(UserIDKey, sub)
		c.Next()
	}
}
