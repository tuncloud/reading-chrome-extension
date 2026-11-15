-- Sync jobs name the user, not the installation: the worker looks up the user's active
-- installation when a job runs. Dead jobs are left alone.

-- Article and day jobs whose installation is gone have no user to run for.
DELETE FROM sync_jobs j
WHERE j.state <> 'dead' AND j.body->>'type' IN ('article', 'day')
  AND NOT EXISTS (SELECT 1 FROM installations i WHERE i.id = j.body->>'installation_id');

-- The rest swap installation_id for the installation's user_id, join that user's group,
-- and get a dedup_id unique by construction.
UPDATE sync_jobs j
SET body     = (j.body - 'installation_id') || jsonb_build_object('user_id', i.user_id),
    group_id = 'user#' || i.user_id,
    dedup_id = 'mig4#' || j.id
FROM installations i
WHERE j.state <> 'dead' AND j.body->>'type' IN ('article', 'day')
  AND i.id = j.body->>'installation_id';

-- Purge jobs already name their user; they only change group.
UPDATE sync_jobs
SET group_id = 'user#' || (body->>'user_id')
WHERE state <> 'dead' AND body->>'type' = 'purge';

-- Rows the 0003 merge left pending have no job (the old ones named an installation that
-- may no longer be active), so every unsynced article and day gets one.
INSERT INTO sync_jobs (group_id, dedup_id, body, run_at)
SELECT 'user#' || user_id, 'mig4#article#' || user_id || '#' || url_hash,
       jsonb_build_object('type', 'article', 'user_id', user_id, 'url_hash', url_hash), now()
FROM articles
WHERE version > COALESCE(notion_synced_version, 0);

INSERT INTO sync_jobs (group_id, dedup_id, body, run_at)
SELECT 'user#' || user_id, 'mig4#day#' || user_id || '#' || date,
       jsonb_build_object('type', 'day', 'user_id', user_id, 'date', date), now()
FROM days
WHERE version > COALESCE(notion_synced_version, 0);
