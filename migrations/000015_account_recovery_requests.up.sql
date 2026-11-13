-- Réplica de las solicitudes de recuperación de cuenta (users.account_recovery_requested
-- de identify) que el staff revisa en el backoffice. La resolución es de admin:
-- al aprobar o rechazar se marca acá y se avisa a identify con un evento firmado.
-- Sin FK a users: la solicitud puede llegar antes que el alta del usuario.
CREATE TABLE IF NOT EXISTS account_recovery_requests (
    request_id   UUID PRIMARY KEY,
    user_id      UUID NOT NULL,
    message      TEXT NOT NULL DEFAULT '',
    status       TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'rejected')),
    requested_at TIMESTAMPTZ NOT NULL,
    resolved_at  TIMESTAMPTZ,
    -- Miembro del staff que la resolvió.
    resolved_by  UUID,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT account_recovery_requests_resolution_consistent
        CHECK ((status = 'pending') = (resolved_at IS NULL))
);

CREATE INDEX IF NOT EXISTS idx_account_recovery_requests_status
    ON account_recovery_requests (status, requested_at DESC);
CREATE INDEX IF NOT EXISTS idx_account_recovery_requests_user
    ON account_recovery_requests (user_id, requested_at DESC);
