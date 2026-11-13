package model

import "time"

type RecoveryRequestStatus string

const (
	RecoveryRequestPending  RecoveryRequestStatus = "pending"
	RecoveryRequestApproved RecoveryRequestStatus = "approved"
	RecoveryRequestRejected RecoveryRequestStatus = "rejected"
)

// Valid dice si s es un estado conocido.
func (s RecoveryRequestStatus) Valid() bool {
	return s == RecoveryRequestPending || s == RecoveryRequestApproved || s == RecoveryRequestRejected
}

// StaffRef es un miembro del staff tal como se muestra en una solicitud.
type StaffRef struct {
	ID string `json:"id"`
	// Name es null si el usuario del staff no está en la réplica.
	Name *string `json:"name"`
}

// RecoveryRequest es una solicitud de un usuario que perdió su segundo factor
// para que el staff le desactive el 2FA.
type RecoveryRequest struct {
	ID string `json:"id"`
	// User es null si el alta del usuario todavía no llegó a la réplica.
	User   *User  `json:"user"`
	UserID string `json:"user_id"`
	// Message es lo que contó el usuario, o vacío si no escribió nada.
	Message     string                `json:"message"`
	Status      RecoveryRequestStatus `json:"status"`
	RequestedAt time.Time             `json:"requested_at"`
	// ResolvedAt y ResolvedBy son null mientras esté pendiente.
	ResolvedAt *time.Time `json:"resolved_at"`
	ResolvedBy *StaffRef  `json:"resolved_by"`
}

// RecoveryRequestList es una página del listado de solicitudes.
type RecoveryRequestList struct {
	Requests []RecoveryRequest `json:"requests"`
	Total    int               `json:"total"`
	Limit    int               `json:"limit"`
	Offset   int               `json:"offset"`
}

// PreviousRecoveryRequests cuenta las solicitudes anteriores del mismo usuario.
type PreviousRecoveryRequests struct {
	Total    int `json:"total"`
	Rejected int `json:"rejected"`
}

// RecoveryRequestDetail es una solicitud con el contexto para decidir.
type RecoveryRequestDetail struct {
	RecoveryRequest
	Previous PreviousRecoveryRequests `json:"previous_requests"`
}
