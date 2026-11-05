DROP INDEX IF EXISTS idx_servers_suspended;
ALTER TABLE servers DROP CONSTRAINT IF EXISTS servers_suspension_consistent;
ALTER TABLE servers
    DROP COLUMN IF EXISTS suspended_by,
    DROP COLUMN IF EXISTS suspended_at;
