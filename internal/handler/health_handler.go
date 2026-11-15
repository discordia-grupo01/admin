// Package handler tiene los handlers HTTP (Gin) y el router de admin.
package handler

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/discordia-grupo01/admin/internal/logging"
)

const readyzTimeout = 3 * time.Second

// statusResponse es el cuerpo de /livez y de /readyz cuando todo está bien.
type statusResponse struct {
	Status string `json:"status" example:"alive"`
}

// unavailableResponse es el cuerpo de /readyz cuando falla alguna dependencia.
type unavailableResponse struct {
	Status string   `json:"status" example:"unavailable"`
	Failed []string `json:"failed" example:"postgres"`
}

// Check es una dependencia crítica que /readyz verifica.
type Check struct {
	Name string
	Fn   func(ctx context.Context) error
}

type HealthHandler struct {
	checks []Check
}

func NewHealthHandler(checks ...Check) *HealthHandler {
	return &HealthHandler{checks: checks}
}

// Livez confirma solo que el proceso está vivo. No toca ninguna dependencia ni
// comparte lógica con Readyz: si Postgres cae, el proceso sigue "vivo" y el
// orquestador no lo reinicia en loop.
//
// @Summary      Liveness check
// @Description  Confirma que el proceso está vivo. No consulta dependencias.
// @Tags         Health
// @Produce      json
// @Success      200  {object}  statusResponse  "El proceso está vivo"
// @Router       /livez [get]
func (h *HealthHandler) Livez(c *gin.Context) {
	c.JSON(http.StatusOK, statusResponse{Status: "alive"})
}

// Readyz confirma que las dependencias críticas responden. Responde 503 con los
// nombres de las que fallan; el detalle del error va al log, no a la respuesta.
//
// @Summary      Readiness check
// @Description  Confirma que las dependencias críticas (Postgres, RabbitMQ) responden.
// @Tags         Health
// @Produce      json
// @Success      200  {object}  statusResponse       "Listo para recibir tráfico"
// @Failure      503  {object}  unavailableResponse  "Alguna dependencia no responde"
// @Router       /readyz [get]
func (h *HealthHandler) Readyz(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), readyzTimeout)
	defer cancel()

	failed := []string{}
	for _, check := range h.checks {
		if err := check.Fn(ctx); err != nil {
			logging.Errorf("readyz: falló %s: %v", check.Name, err)
			failed = append(failed, check.Name)
		}
	}
	if len(failed) > 0 {
		c.JSON(http.StatusServiceUnavailable, unavailableResponse{Status: "unavailable", Failed: failed})
		return
	}
	c.JSON(http.StatusOK, statusResponse{Status: "ready"})
}
