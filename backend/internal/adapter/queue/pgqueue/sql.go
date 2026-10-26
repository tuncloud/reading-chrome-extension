package pgqueue

const sqlInsert = `INSERT INTO sync_jobs (group_id, dedup_id, body, run_at)
VALUES ($1, $2, $3, now() + $4::interval)
ON CONFLICT (dedup_id) DO NOTHING`

// sqlClaim is design §5.2 verbatim: only a group's head is ever a candidate.
const sqlClaim = `WITH candidate AS (
  SELECT id FROM sync_jobs j
  WHERE (state = 'ready' OR (state = 'leased' AND leased_until <= now()))
    AND run_at <= now()
    AND id = (SELECT min(id) FROM sync_jobs h
              WHERE h.group_id = j.group_id AND h.state <> 'dead')
  ORDER BY run_at, id
  FOR UPDATE SKIP LOCKED
  LIMIT 1)
UPDATE sync_jobs SET state='leased', leased_until = now() + interval '6 minutes', attempts = attempts + 1
FROM candidate WHERE sync_jobs.id = candidate.id
RETURNING sync_jobs.*`

const sqlAck = `DELETE FROM sync_jobs WHERE id = $1`

const sqlRelease = `UPDATE sync_jobs SET state = 'ready', leased_until = NULL, run_at = now() + $3::interval, last_error = $2
WHERE id = $1`

const sqlDead = `UPDATE sync_jobs SET state = 'dead', leased_until = NULL, last_error = $2 WHERE id = $1`

const sqlSweepDead = `DELETE FROM sync_jobs WHERE state = 'dead' AND created_at < now() - interval '14 days'`
