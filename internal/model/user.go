// Package model tiene los tipos de dominio y las respuestas de la API.
package model

import "time"

type UserStatus string

const (
	UserStatusActive    UserStatus = "active"
	UserStatusSuspended UserStatus = "suspended"
)

// Valid dice si s es un estado conocido.
func (s UserStatus) Valid() bool {
	return s == UserStatusActive || s == UserStatusSuspended
}

// User es un usuario tal como lo ve el backoffice.
type User struct {
	ID     string     `json:"id"`
	Name   string     `json:"name"`
	Email  string     `json:"email"`
	Status UserStatus `json:"status"`
	// CreatedAt es la fecha de registro de la cuenta (UTC).
	CreatedAt time.Time `json:"created_at"`
	// SuspendedAt es null si el usuario está activo.
	SuspendedAt *time.Time `json:"suspended_at"`
}

// UserList es una página del listado de usuarios.
type UserList struct {
	Users  []User `json:"users"`
	Total  int    `json:"total"`
	Limit  int    `json:"limit"`
	Offset int    `json:"offset"`
}
