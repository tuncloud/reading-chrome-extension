CREATE TABLE sync_jobs (
  id           bigserial PRIMARY KEY,
  group_id     text        NOT NULL,            -- Message.GroupID()
  dedup_id     text        NOT NULL,            -- Outgoing.DedupID
  body         jsonb       NOT NULL,            -- queue.Message, same JSON as the SQS body
  state        text        NOT NULL DEFAULT 'ready',  -- ready | leased | dead
  run_at       timestamptz NOT NULL,
  attempts     int         NOT NULL DEFAULT 0,
  leased_until timestamptz,
  created_at   timestamptz NOT NULL DEFAULT now(),
  last_error   text
);
CREATE UNIQUE INDEX sync_jobs_dedup ON sync_jobs (dedup_id);
CREATE INDEX sync_jobs_head  ON sync_jobs (group_id, id) WHERE state <> 'dead';
CREATE INDEX sync_jobs_ready ON sync_jobs (run_at) WHERE state = 'ready';
