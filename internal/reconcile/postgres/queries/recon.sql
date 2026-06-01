-- name: InsertReconRun :one
INSERT INTO recon_runs (internal_source, external_source, status)
VALUES ($1, $2, 'running')
RETURNING *;

-- name: FinishReconRun :one
UPDATE recon_runs
SET finished_at     = now(),
    status          = 'completed',
    matched_count   = $2,
    exception_count = $3,
    summary         = $4
WHERE id = $1
RETURNING *;

-- name: InsertReconException :one
INSERT INTO recon_exceptions (run_id, category, reference, internal_record, external_record, delta_minor)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: FindCompletedRunByFingerprint :one
SELECT * FROM recon_runs
WHERE status = 'completed'
  AND summary->>'input_fingerprint' = $1::text
ORDER BY started_at DESC
LIMIT 1;
