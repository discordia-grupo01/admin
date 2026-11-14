// Package model tiene los tipos de dominio y las respuestas de la API.
package model

import "time"

type UserStatus string

const (
	UserStatusActive UserStatus = "active"
	// UserStatusPending es una cuenta que todavía no confirmó su email.
	UserStatusPending   UserStatus = "pending"
	UserStatusSuspended UserStatus = "suspended"
)

// Valid dice si s es un estado conocido.
func (s UserStatus) Valid() bool {
	return s == UserStatusActive || s == UserStatusPending || s == UserStatusSuspended
}

// User es un usuario tal como lo ve el backoffice.
type User struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
	// AvatarURL es la dirección de la foto de perfil tal como la guarda
	// identify (URL pública o ruta del gateway), o null si no tiene o todavía
	// no llegó a la réplica.
	AvatarURL *string    `json:"avatar_url"`
	Status    UserStatus `json:"status"`
	// CreatedAt es la fecha de registro de la cuenta (UTC).
	CreatedAt time.Time `json:"created_at"`
	// SuspendedAt es null si el usuario no está suspendido.
	SuspendedAt *time.Time `json:"suspended_at"`
}

// UserList es una página del listado de usuarios.
type UserList struct {
	Users  []User `json:"users"`
	Total  int    `json:"total"`
	Limit  int    `json:"limit"`
	Offset int    `json:"offset"`
}
