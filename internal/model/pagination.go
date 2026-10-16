package model

const (
	// Misma convención que los listados de servers (GET /members): 20 por
	// defecto y 100 como máximo.
	DefaultLimit = 20
	MaxLimit     = 100
)

// NormalizeLimit aplica el default si limit es 0 y lo acota al máximo.
func NormalizeLimit(limit int) int {
	switch {
	case limit <= 0:
		return DefaultLimit
	case limit > MaxLimit:
		return MaxLimit
	default:
		return limit
	}
}
