-- Runtime SQL template from V2 DBQueryStore.Query:
-- WITH q AS (<runtime read-only SQL>) SELECT * FROM q LIMIT ?;
-- A true sqlc query cannot represent a runtime-supplied SELECT shape, so this
-- file keeps a typed placeholder until sqlc generation is introduced.

-- name: PlaceholderDBQuery :many
SELECT NULL AS placeholder
WHERE FALSE;
