-- SOURCE-001 vertical slice: web observations, local selection metadata and
-- episode selection. No media assets or playback fields are stored here.

CREATE TABLE IF NOT EXISTS ingest_run (
  id BIGINT NOT NULL AUTO_INCREMENT,
  source_code VARCHAR(32) NOT NULL,
  channel VARCHAR(64) NOT NULL,
  page_start INT NOT NULL,
  page_end INT NOT NULL,
  status VARCHAR(16) NOT NULL,
  pages_fetched INT NOT NULL DEFAULT 0,
  items_seen INT NOT NULL DEFAULT 0,
  items_upserted INT NOT NULL DEFAULT 0,
  error_code VARCHAR(64) NOT NULL DEFAULT '',
  error_message VARCHAR(1000) NOT NULL DEFAULT '',
  started_at DATETIME(6) NOT NULL,
  finished_at DATETIME(6) NULL,
  PRIMARY KEY (id),
  KEY idx_ingest_started (source_code, started_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS source_candidate (
  id BIGINT NOT NULL AUTO_INCREMENT,
  source_code VARCHAR(32) NOT NULL,
  external_id VARCHAR(64) NOT NULL,
  title VARCHAR(255) NOT NULL,
  cover_url VARCHAR(512) NOT NULL DEFAULT '',
  description TEXT NOT NULL,
  tags JSON NULL,
  episode_count INT NOT NULL DEFAULT 0,
  open_episode_count INT NOT NULL DEFAULT 0,
  rank_no INT NOT NULL DEFAULT 0,
  metrics JSON NULL,
  content_hash CHAR(64) NOT NULL,
  first_seen_at DATETIME(6) NOT NULL,
  last_seen_at DATETIME(6) NOT NULL,
  last_run_id BIGINT NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uk_candidate (source_code, external_id),
  KEY idx_candidate_seen (last_seen_at),
  KEY idx_candidate_title (title)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS content_series (
  id BIGINT NOT NULL AUTO_INCREMENT,
  source_code VARCHAR(32) NOT NULL,
  external_id VARCHAR(64) NOT NULL,
  candidate_id BIGINT NULL,
  title VARCHAR(255) NOT NULL,
  description TEXT NOT NULL,
  cover_url VARCHAR(512) NOT NULL DEFAULT '',
  status VARCHAR(16) NOT NULL DEFAULT 'draft',
  revision INT NOT NULL DEFAULT 1,
  created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
  PRIMARY KEY (id),
  UNIQUE KEY uk_series (source_code, external_id),
  KEY idx_series_candidate (candidate_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS series_episode (
  id BIGINT NOT NULL AUTO_INCREMENT,
  series_id BIGINT NOT NULL,
  external_id VARCHAR(64) NOT NULL,
  episode_no INT NOT NULL,
  selected TINYINT(1) NOT NULL DEFAULT 0,
  created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
  PRIMARY KEY (id),
  UNIQUE KEY uk_episode (series_id, external_id),
  UNIQUE KEY uk_episode_no (series_id, episode_no),
  KEY idx_episode_selected (series_id, selected)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
