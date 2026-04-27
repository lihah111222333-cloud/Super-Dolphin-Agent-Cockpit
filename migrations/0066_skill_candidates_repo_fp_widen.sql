-- 0066_skill_candidates_repo_fp_widen.sql — P21 Fix-01 T-06
-- Widen repo_fingerprint storage for the canonical 128-bit (32 hex char)
-- fingerprint. VARCHAR(64) leaves room for future algorithm expansion.

ALTER TABLE public.skill_candidates
    ALTER COLUMN repo_fingerprint TYPE VARCHAR(64);
