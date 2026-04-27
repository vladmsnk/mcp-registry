ALTER TABLE tools
    ADD COLUMN IF NOT EXISTS required_roles TEXT[] NOT NULL DEFAULT '{}';

CREATE INDEX IF NOT EXISTS idx_tools_required_roles ON tools USING gin (required_roles);
