package model

import "time"

// Server es un servidor tal como lo ve el backoffice.
type Server struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	OwnerID string `json:"owner_id"`
	// OwnerName es null si el perfil del dueño todavía no llegó a la réplica.
	OwnerName   *string   `json:"owner_name"`
	MemberCount int       `json:"member_count"`
	CreatedAt   time.Time `json:"created_at"`
}

// ServerList es una página del listado de servidores.
type ServerList struct {
	Servers []Server `json:"servers"`
	Total   int      `json:"total"`
	Limit   int      `json:"limit"`
	Offset  int      `json:"offset"`
}
