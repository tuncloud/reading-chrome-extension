-- Sync jobs name the user, not the installation: the worker looks up the user's active
-- installation when a job runs. Dead jobs are left alone.
--
-- Rollout: old pollers may still be running while this migration has run. They cannot
-- read the new bodies (empty installation id) and would acknowledge those jobs without
-- syncing, so every job this migration rewrites (unless leased) or inserts is held back
-- 15 minutes, long enough for the rolling update to replace them.

-- Jobs that cannot be attributed to a user are dropped: article and day jobs whose
-- installation is gone, and purge jobs without a user_id.
DELETE FROM sync_jobs j
WHERE j.state <> 'dead' AND j.body->>'type' IN ('article', 'day')
  AND NOT EXISTS (SELECT 1 FROM installations i WHERE i.id = j.body->>'installation_id');

DELETE FROM sync_jobs
WHERE state <> 'dead' AND body->>'type' = 'purge' AND COALESCE(body->>'user_id', '') = '';

-- The rest swap installation_id for the installation's user_id, join that user's group,
-- and get a dedup_id unique by construction.
UPDATE sync_jobs j
SET body     = (j.body - 'installation_id') || jsonb_build_object('user_id', i.user_id),
    group_id = 'user#' || i.user_id,
    dedup_id = 'mig4#' || j.id,
    run_at   = CASE WHEN j.state = 'leased' THEN j.run_at ELSE GREATEST(j.run_at, now() + interval '15 minutes') END
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
       jsonb_build_object('type', 'article', 'user_id', user_id, 'url_hash', url_hash), now() + interval '15 minutes'
FROM articles
WHERE version > COALESCE(notion_synced_version, 0);

INSERT INTO sync_jobs (group_id, dedup_id, body, run_at)
SELECT 'user#' || user_id, 'mig4#day#' || user_id || '#' || date,
       jsonb_build_object('type', 'day', 'user_id', user_id, 'date', date), now() + interval '15 minutes'
FROM days
WHERE version > COALESCE(notion_synced_version, 0);
