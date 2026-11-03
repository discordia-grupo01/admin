ALTER TABLE servers
    DROP COLUMN IF EXISTS deletion_requested_by,
    DROP COLUMN IF EXISTS deletion_requested_at,
    DROP COLUMN IF EXISTS moderation_version;
