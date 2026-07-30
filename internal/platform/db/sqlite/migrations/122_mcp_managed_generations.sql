CREATE TABLE mcp_managed_generation_owner (
    singleton_id INTEGER PRIMARY KEY CHECK (singleton_id = 1),
    owner_epoch TEXT NOT NULL CHECK (length(owner_epoch) = 64),
    marker_initialized INTEGER NOT NULL DEFAULT 0 CHECK (marker_initialized IN (0, 1)),
    ledger_initialized INTEGER NOT NULL DEFAULT 0 CHECK (ledger_initialized IN (0, 1))
);

INSERT INTO mcp_managed_generation_owner(singleton_id, owner_epoch, marker_initialized)
VALUES (1, lower(hex(randomblob(32))), 0);

CREATE TABLE mcp_managed_generation_instances (
    instance_id TEXT PRIMARY KEY CHECK (length(trim(instance_id)) > 0)
);

CREATE TABLE mcp_managed_generations (
    instance_id TEXT PRIMARY KEY
        REFERENCES mcp_managed_generation_instances(instance_id)
        ON DELETE RESTRICT,
    generation INTEGER NOT NULL CHECK (generation > 0),
    claim_id TEXT NOT NULL CHECK (length(claim_id) = 64),
    external_committed INTEGER NOT NULL DEFAULT 0 CHECK (external_committed IN (0, 1))
);
