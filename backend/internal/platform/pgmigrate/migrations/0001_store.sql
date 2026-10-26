CREATE TABLE users (id text PRIMARY KEY, notion_user_id text NOT NULL, name text NOT NULL, email text,
  active_installation_id text, created_at timestamptz NOT NULL, deleted_at timestamptz);
CREATE TABLE notion_user_links (notion_user_id text PRIMARY KEY, user_id text NOT NULL);
CREATE TABLE user_installations (user_id text, installation_id text, created_at timestamptz NOT NULL,
  PRIMARY KEY (user_id, installation_id));
CREATE TABLE devices (user_id text, id text, name text NOT NULL, token_hmac text NOT NULL,
  created_at timestamptz NOT NULL, last_seen_at timestamptz, revoked_at timestamptz, PRIMARY KEY (user_id, id));
CREATE TABLE device_tokens (token_hmac text PRIMARY KEY, user_id text NOT NULL, device_id text NOT NULL);
CREATE TABLE oauth_states (state text PRIMARY KEY, ext_redirect text NOT NULL, code_challenge text NOT NULL,
  expires_at timestamptz NOT NULL);
CREATE TABLE auth_codes (code text PRIMARY KEY, user_id text NOT NULL, installation_id text NOT NULL,
  code_challenge text NOT NULL, ext_redirect text NOT NULL, expires_at timestamptz NOT NULL);
CREATE TABLE installations (id text PRIMARY KEY, user_id text NOT NULL, workspace_id text NOT NULL,
  workspace_name text NOT NULL, workspace_icon text, status text NOT NULL
    CHECK (status IN ('active','needs_setup','revoked')), setup_error text,
  access_token_enc text NOT NULL, refresh_token_enc text NOT NULL, token_expires_at text,
  token_version int NOT NULL, duplicated_template_id text, library_db_id text, library_ds_id text,
  library_url text, daily_db_id text, daily_ds_id text, props jsonb,
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL);
CREATE TABLE articles (installation_id text, url_hash text, canonical_url text NOT NULL, source text NOT NULL,
  title text NOT NULL, author text NOT NULL, site_name text NOT NULL, word_count int NOT NULL,
  published_at text NOT NULL, first_read_at timestamptz, last_read_at timestamptz,
  total_seconds bigint NOT NULL DEFAULT 0, sessions bigint NOT NULL DEFAULT 0,
  max_progress double precision, completed boolean NOT NULL DEFAULT false, version bigint NOT NULL DEFAULT 0,
  notion_page_id text, notion_url text, notion_ds_id text, notion_synced_version bigint,
  notion_synced_at timestamptz, PRIMARY KEY (installation_id, url_hash));
CREATE TABLE segments (installation_id text, id text, session_id text NOT NULL, seq int NOT NULL,
  url_hash text NOT NULL, device_id text NOT NULL, started_at timestamptz NOT NULL, ended_at timestamptz NOT NULL,
  active_seconds int NOT NULL, max_progress double precision NOT NULL, expires_at timestamptz NOT NULL,
  PRIMARY KEY (installation_id, id));
CREATE TABLE days (installation_id text, date text, seconds bigint NOT NULL DEFAULT 0,
  article_hashes text[] NOT NULL DEFAULT '{}', version bigint NOT NULL DEFAULT 0, notion_page_id text,
  notion_ds_id text, notion_synced_version bigint, PRIMARY KEY (installation_id, date));
CREATE INDEX segments_expiry ON segments (expires_at);
