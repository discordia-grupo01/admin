package model

import "time"

// Secciones del expediente que se pueden paginar.
const (
	SectionServers    = "servers"
	SectionActivity   = "activity"
	SectionReports    = "reports"
	SectionModeration = "moderation"
	// SectionRecoveryRequests son las solicitudes de recuperación de cuenta
	// (sección "Seguridad de la cuenta"). Solo existe en el expediente de usuario.
	SectionRecoveryRequests = "recovery_requests"
)

// DossierPreviewSize es cuántos ítems de cada sección trae el resumen.
const DossierPreviewSize = 5

// Tipos de actividad.
const (
	ActivityMessageSent  = "message_sent"
	ActivityJoinedServer = "joined_server"
	ActivityLeftServer   = "left_server"
)

// Alcance de una acción de moderación: la hizo el staff de la plataforma o un
// moderador de un servidor.
const (
	ScopePlatform = "platform"
	ScopeServer   = "server"
)

// DossierPage es una página de una sección del expediente.
type DossierPage[T any] struct {
	Items  []T `json:"items"`
	Total  int `json:"total"`
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

// DossierMembership es un servidor al que pertenece el usuario.
type DossierMembership struct {
	ServerID string `json:"server_id" format:"uuid"`
	// ServerName es null si la foto del servidor todavía no llegó a la réplica.
	ServerName *string `json:"server_name" extensions:"x-nullable"`
	// ServerIconURL es la dirección del ícono tal como la guarda servers, o
	// null si no tiene o todavía no llegó a la réplica.
	ServerIconURL *string   `json:"server_icon_url" extensions:"x-nullable"`
	JoinedAt      time.Time `json:"joined_at" format:"date-time"`
}

// DossierActivity es algo que hizo el usuario. Nunca incluye el contenido de un
// mensaje.
type DossierActivity struct {
	ID          string    `json:"id" format:"uuid"`
	Kind        string    `json:"kind" enums:"message_sent,joined_server,left_server"`
	Description string    `json:"description" example:"Envió un mensaje en #general de GamersHub"`
	ServerID    string    `json:"server_id" format:"uuid"`
	OccurredAt  time.Time `json:"occurred_at" format:"date-time"`
}

// DossierReport es un reporte de un servidor. No incluye quién reportó ni quién
// lo resolvió. En un reporte de mensaje, ReportedUserID es el autor del mensaje
// si admin lo conoce, y null si no.
type DossierReport struct {
	ID               string     `json:"id" format:"uuid"`
	Reason           string     `json:"reason"`
	CreatedAt        time.Time  `json:"created_at" format:"date-time"`
	Kind             string     `json:"kind" enums:"user,message"`
	Status           string     `json:"status" enums:"pending,resolved,dismissed"`
	ResolvedAt       *time.Time `json:"resolved_at" format:"date-time" extensions:"x-nullable"`
	ServerID         string     `json:"server_id" format:"uuid"`
	ServerName       *string    `json:"server_name" extensions:"x-nullable"`
	ReportedUserID   *string    `json:"reported_user_id" format:"uuid" extensions:"x-nullable"`
	ReportedUserName *string    `json:"reported_user_name" extensions:"x-nullable"`
}

// DossierModerationAction es una acción de moderación. No incluye quién la hizo.
type DossierModerationAction struct {
	ID             string    `json:"id" format:"uuid"`
	Kind           string    `json:"kind" enums:"suspension,reactivation,deletion_requested,ban,unban,kick,mute,unmute"`
	AppliedAt      time.Time `json:"applied_at" format:"date-time"`
	Scope          string    `json:"scope" enums:"platform,server"`
	ServerID       *string   `json:"server_id" format:"uuid" extensions:"x-nullable"`
	ServerName     *string   `json:"server_name" extensions:"x-nullable"`
	TargetUserID   *string   `json:"target_user_id" format:"uuid" extensions:"x-nullable"`
	TargetUserName *string   `json:"target_user_name" extensions:"x-nullable"`
}

// DossierRecoveryRequest es una solicitud de recuperación de cuenta del
// usuario. No es una acción de moderación: va en su propia sección.
type DossierRecoveryRequest struct {
	ID          string                `json:"id" format:"uuid"`
	Status      RecoveryRequestStatus `json:"status"`
	RequestedAt time.Time             `json:"requested_at" format:"date-time"`
	ResolvedAt  *time.Time            `json:"resolved_at" format:"date-time" extensions:"x-nullable"`
	ResolvedBy  *StaffRef             `json:"resolved_by" extensions:"x-nullable"`
}

// MessagingDay son los mensajes enviados en un servidor en un día UTC.
type MessagingDay struct {
	Date     string `json:"date" format:"date" example:"2026-11-08"` // YYYY-MM-DD, día UTC
	Messages int    `json:"messages" minimum:"0"`
}

type UserDossierCounts struct {
	Servers          int `json:"servers"`
	Activity         int `json:"activity"`
	Reports          int `json:"reports"`
	Moderation       int `json:"moderation"`
	RecoveryRequests int `json:"recovery_requests"`
}

type UserDossierLatest struct {
	Servers          []DossierMembership       `json:"servers"`
	Activity         []DossierActivity         `json:"activity"`
	Reports          []DossierReport           `json:"reports"`
	Moderation       []DossierModerationAction `json:"moderation"`
	RecoveryRequests []DossierRecoveryRequest  `json:"recovery_requests"`
}

// UserDossierSummary es el resumen del expediente de un usuario.
type UserDossierSummary struct {
	User *User `json:"user"`
	// LastActivityAt es null si no hay actividad registrada.
	LastActivityAt *time.Time        `json:"last_activity_at" format:"date-time" extensions:"x-nullable"`
	Counts         UserDossierCounts `json:"counts"`
	Latest         UserDossierLatest `json:"latest"`
}

type ServerDossierCounts struct {
	Reports    int `json:"reports"`
	Moderation int `json:"moderation"`
}

type ServerDossierLatest struct {
	Reports    []DossierReport           `json:"reports"`
	Moderation []DossierModerationAction `json:"moderation"`
}

// ServerDossierSummary es el resumen del expediente de un servidor.
type ServerDossierSummary struct {
	Server *Server `json:"server"`
	// Messaging son los últimos 7 días UTC, del más viejo al más nuevo, con ceros.
	Messaging []MessagingDay      `json:"messaging" minItems:"7" maxItems:"7"`
	Counts    ServerDossierCounts `json:"counts"`
	Latest    ServerDossierLatest `json:"latest"`
}
