package model

import "time"

// ServerStatus es el estado de un servidor para el backoffice. No se guarda: se
// deriva de si tiene una suspensión vigente (Server.SuspendedAt).
type ServerStatus string

const (
	ServerStatusActive    ServerStatus = "active"
	ServerStatusSuspended ServerStatus = "suspended"
)

// Valid dice si s es un estado conocido.
func (s ServerStatus) Valid() bool {
	return s == ServerStatusActive || s == ServerStatusSuspended
}

// Server es un servidor tal como lo ve el backoffice.
type Server struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// IconURL es la dirección del ícono tal como la guarda servers (URL pública
	// o ruta del gateway), o null si no tiene o todavía no llegó a la réplica.
	IconURL *string `json:"icon_url"`
	OwnerID string  `json:"owner_id"`
	// OwnerName es null si el perfil del dueño todavía no llegó a la réplica.
	OwnerName   *string   `json:"owner_name"`
	MemberCount int       `json:"member_count"`
	CreatedAt   time.Time `json:"created_at"`
	// SuspendedAt es null si el servidor no está suspendido.
	SuspendedAt *time.Time `json:"suspended_at"`
}

// ServerList es una página del listado de servidores.
type ServerList struct {
	Servers []Server `json:"servers"`
	Total   int      `json:"total"`
	Limit   int      `json:"limit"`
	Offset  int      `json:"offset"`
}
