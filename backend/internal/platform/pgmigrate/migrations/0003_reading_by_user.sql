-- 0003: reading data belongs to the user, not to one Notion authorization.
--
-- articles, segments and days are re-keyed from installation_id to user_id (the
-- installation's user). Each new table is built beside the old one, then the old one is
-- dropped and the new one renamed, so the file runs inside pgmigrate's one transaction.
--
-- Rules:
--   * A row whose installation no longer exists cannot be attributed and is dropped.
--   * Rows of one user for the same article (or day) merge into one: counters and
--     version are summed, max_progress and last_read_at take the maximum, first_read_at
--     the minimum, completed is true if any was, article_hashes is the union.
--   * Descriptive article columns come from the row read first (earliest first_read_at).
--   * Notion columns come from the row of the user's active installation; without one
--     they are all NULL, so the row is synced afresh. Summing versions keeps version
--     above any notion_synced_version kept, so the next sync writes the merged totals.
--   * A segment id present under several installations of one user is kept once.

-- Writers from old server pods must wait here, then fail against the new schema (the
-- extension retries); otherwise a push committed mid-migration would be lost. Reads stay allowed.
LOCK TABLE articles, days, segments IN EXCLUSIVE MODE;

-- owned_* attach the user to each row and flag the one from the active installation.
-- A row whose installation's user has no users row is kept under that user id.
CREATE TEMP TABLE owned_articles ON COMMIT DROP AS
SELECT i.user_id, (i.id = u.active_installation_id) IS TRUE AS from_active, a.*
FROM articles a
JOIN installations i ON i.id = a.installation_id
LEFT JOIN users u ON u.id = i.user_id;

CREATE TEMP TABLE owned_days ON COMMIT DROP AS
SELECT i.user_id, (i.id = u.active_installation_id) IS TRUE AS from_active, d.*
FROM days d
JOIN installations i ON i.id = d.installation_id
LEFT JOIN users u ON u.id = i.user_id;

-- articles ------------------------------------------------------------------------------

CREATE TABLE articles_by_user (user_id text, url_hash text, canonical_url text NOT NULL, source text NOT NULL,
  title text NOT NULL, author text NOT NULL, site_name text NOT NULL, word_count int NOT NULL,
  published_at text NOT NULL, first_read_at timestamptz, last_read_at timestamptz,
  total_seconds bigint NOT NULL DEFAULT 0, sessions bigint NOT NULL DEFAULT 0,
  max_progress double precision, completed boolean NOT NULL DEFAULT false, version bigint NOT NULL DEFAULT 0,
  notion_page_id text, notion_url text, notion_ds_id text, notion_synced_version bigint,
  notion_synced_at timestamptz, PRIMARY KEY (user_id, url_hash));

INSERT INTO articles_by_user
WITH totals AS (
  SELECT user_id, url_hash,
    min(first_read_at) AS first_read_at, max(last_read_at) AS last_read_at,
    sum(total_seconds)::bigint AS total_seconds, sum(sessions)::bigint AS sessions,
    max(max_progress) AS max_progress, bool_or(completed) AS completed, sum(version)::bigint AS version
  FROM owned_articles GROUP BY user_id, url_hash
), described AS (
  SELECT DISTINCT ON (user_id, url_hash) user_id, url_hash,
    canonical_url, source, title, author, site_name, word_count, published_at
  FROM owned_articles
  ORDER BY user_id, url_hash, first_read_at NULLS LAST, installation_id
), synced AS (
  SELECT user_id, url_hash, notion_page_id, notion_url, notion_ds_id, notion_synced_version, notion_synced_at
  FROM owned_articles WHERE from_active
)
SELECT t.user_id, t.url_hash, d.canonical_url, d.source, d.title, d.author, d.site_name, d.word_count,
  d.published_at, t.first_read_at, t.last_read_at, t.total_seconds, t.sessions, t.max_progress, t.completed,
  t.version, s.notion_page_id, s.notion_url, s.notion_ds_id, s.notion_synced_version, s.notion_synced_at
FROM totals t
JOIN described d USING (user_id, url_hash)
LEFT JOIN synced s USING (user_id, url_hash);

DROP TABLE articles;
ALTER TABLE articles_by_user RENAME TO articles;
ALTER INDEX articles_by_user_pkey RENAME TO articles_pkey;
CREATE INDEX articles_user_last_read ON articles (user_id, last_read_at DESC NULLS LAST, url_hash);

-- days ----------------------------------------------------------------------------------

CREATE TABLE days_by_user (user_id text, date text, seconds bigint NOT NULL DEFAULT 0,
  article_hashes text[] NOT NULL DEFAULT '{}', version bigint NOT NULL DEFAULT 0, notion_page_id text,
  notion_ds_id text, notion_synced_version bigint, PRIMARY KEY (user_id, date));

INSERT INTO days_by_user
WITH totals AS (
  SELECT user_id, date, sum(seconds)::bigint AS seconds, sum(version)::bigint AS version
  FROM owned_days GROUP BY user_id, date
), first_seen AS (
  -- Each hash once, at its first position (by installation id, then array order), so an
  -- unmerged day keeps its order.
  SELECT DISTINCT ON (o.user_id, o.date, x.hash) o.user_id, o.date, x.hash, o.installation_id, x.pos
  FROM owned_days o CROSS JOIN LATERAL unnest(o.article_hashes) WITH ORDINALITY AS x(hash, pos)
  ORDER BY o.user_id, o.date, x.hash, o.installation_id, x.pos
), hashes AS (
  SELECT user_id, date, array_agg(hash ORDER BY installation_id, pos) AS article_hashes
  FROM first_seen GROUP BY user_id, date
), synced AS (
  SELECT user_id, date, notion_page_id, notion_ds_id, notion_synced_version
  FROM owned_days WHERE from_active
)
SELECT t.user_id, t.date, t.seconds, COALESCE(h.article_hashes, '{}'), t.version,
  s.notion_page_id, s.notion_ds_id, s.notion_synced_version
FROM totals t
LEFT JOIN hashes h USING (user_id, date)
LEFT JOIN synced s USING (user_id, date);

DROP TABLE days;
ALTER TABLE days_by_user RENAME TO days;
ALTER INDEX days_by_user_pkey RENAME TO days_pkey;

-- segments ------------------------------------------------------------------------------

CREATE TABLE segments_by_user (user_id text, id text, session_id text NOT NULL, seq int NOT NULL,
  url_hash text NOT NULL, device_id text NOT NULL, started_at timestamptz NOT NULL, ended_at timestamptz NOT NULL,
  active_seconds int NOT NULL, max_progress double precision NOT NULL, expires_at timestamptz NOT NULL,
  PRIMARY KEY (user_id, id));

INSERT INTO segments_by_user
SELECT DISTINCT ON (i.user_id, s.id) i.user_id, s.id, s.session_id, s.seq, s.url_hash, s.device_id,
  s.started_at, s.ended_at, s.active_seconds, s.max_progress, s.expires_at
FROM segments s
JOIN installations i ON i.id = s.installation_id
ORDER BY i.user_id, s.id, s.installation_id;

DROP TABLE segments;
ALTER TABLE segments_by_user RENAME TO segments;
ALTER INDEX segments_by_user_pkey RENAME TO segments_pkey;
CREATE INDEX segments_expiry ON segments (expires_at);
