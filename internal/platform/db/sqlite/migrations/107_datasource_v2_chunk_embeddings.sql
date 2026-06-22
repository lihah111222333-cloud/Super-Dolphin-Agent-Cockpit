ALTER TABLE datasource_v2_text_chunks ADD COLUMN embedding BLOB;
-- SPLIT --
ALTER TABLE datasource_v2_text_chunks ADD COLUMN embedding_model TEXT NOT NULL DEFAULT '';
-- SPLIT --
ALTER TABLE datasource_v2_text_chunks ADD COLUMN embedding_dim INTEGER NOT NULL DEFAULT 0;
-- SPLIT --
ALTER TABLE datasource_v2_text_chunks ADD COLUMN token_count INTEGER NOT NULL DEFAULT 0;
