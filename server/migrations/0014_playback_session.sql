-- 0014_playback_session.sql
--
-- Anonymous playback sessions. A session is bound to one publication, one
-- episode and one asset version; refreshing re-signs the same object and can
-- never switch to another episode.
--
-- The token is stored as a hash only, like the staff session, so a database
-- read cannot be replayed as a guest session.

CREATE TABLE IF NOT EXISTS playback_session (
  id BIGINT NOT NULL AUTO_INCREMENT,
  token_hash CHAR(64) NOT NULL,
  publication_id BIGINT NOT NULL,
  series_id BIGINT NOT NULL,
  revision_id BIGINT NOT NULL,
  episode_id BIGINT NOT NULL,
  asset_id BIGINT NOT NULL,
  object_provider VARCHAR(16) NOT NULL DEFAULT '',
  object_bucket VARCHAR(128) NOT NULL DEFAULT '',
  object_region VARCHAR(64) NOT NULL DEFAULT '',
  object_key VARCHAR(512) NOT NULL DEFAULT '',
  object_version VARCHAR(128) NOT NULL DEFAULT '',
  expires_at DATETIME(6) NOT NULL,
  created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
  PRIMARY KEY (id),
  UNIQUE KEY uk_playback_session_token (token_hash),
  KEY idx_playback_session_publication (publication_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
