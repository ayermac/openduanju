-- Correct SOURCE-001 to the agreed model: persistent async runs, complete
-- candidate snapshots ingested during collection, and revision-based local
-- selection. DDL is additive and guarded so a failed attempt can be rerun.

CREATE TABLE IF NOT EXISTS ingestion_plan (
  id BIGINT NOT NULL AUTO_INCREMENT,
  code VARCHAR(64) NOT NULL,
  name VARCHAR(128) NOT NULL,
  source_code VARCHAR(32) NOT NULL,
  channel VARCHAR(64) NOT NULL,
  page_start INT NOT NULL,
  page_end INT NOT NULL,
  request_budget INT NOT NULL DEFAULT 80,
  item_budget INT NOT NULL DEFAULT 200,
  fetch_details TINYINT(1) NOT NULL DEFAULT 1,
  fetch_episodes TINYINT(1) NOT NULL DEFAULT 1,
  enabled TINYINT(1) NOT NULL DEFAULT 1,
  version INT NOT NULL DEFAULT 1,
  created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
  PRIMARY KEY (id),
  UNIQUE KEY uk_plan_code (code)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS ingestion_run_page (
  id BIGINT NOT NULL AUTO_INCREMENT,
  run_id BIGINT NOT NULL,
  page_no INT NOT NULL,
  status VARCHAR(16) NOT NULL DEFAULT 'pending',
  items_seen INT NOT NULL DEFAULT 0,
  error_code VARCHAR(64) NOT NULL DEFAULT '',
  fetched_at DATETIME(6) NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uk_run_page (run_id, page_no)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

SET @ddl = IF(
  (SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = 'ingest_run') = 1
  AND (SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = 'ingestion_run') = 0,
  'RENAME TABLE ingest_run TO ingestion_run',
  'DO 0'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

SET @ddl = IF(
  (SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = 'ingestion_run' AND column_name = 'plan_id') = 0,
  'ALTER TABLE ingestion_run
     ADD COLUMN plan_id BIGINT NOT NULL DEFAULT 0,
     ADD COLUMN plan_snapshot JSON NULL,
     ADD COLUMN kind VARCHAR(16) NOT NULL DEFAULT ''ranking'',
     ADD COLUMN candidate_id BIGINT NULL,
     ADD COLUMN request_budget INT NOT NULL DEFAULT 0,
     ADD COLUMN item_budget INT NOT NULL DEFAULT 0,
     ADD COLUMN requests_used INT NOT NULL DEFAULT 0,
     ADD COLUMN page_cursor INT NOT NULL DEFAULT 0,
     ADD COLUMN detail_fetched INT NOT NULL DEFAULT 0,
     ADD COLUMN episodes_fetched INT NOT NULL DEFAULT 0,
     ADD COLUMN cancel_requested TINYINT(1) NOT NULL DEFAULT 0,
     ADD COLUMN lease_owner VARCHAR(64) NOT NULL DEFAULT '''',
     ADD COLUMN lease_until DATETIME(6) NULL,
     ADD COLUMN next_request_at DATETIME(6) NULL,
     ADD COLUMN version INT NOT NULL DEFAULT 1,
     ADD COLUMN updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6)',
  'DO 0'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

SET @ddl = IF(
  (SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = 'source_candidate' AND column_name = 'completeness') = 0,
  'ALTER TABLE source_candidate
     ADD COLUMN channel VARCHAR(64) NOT NULL DEFAULT '''',
     ADD COLUMN completeness VARCHAR(16) NOT NULL DEFAULT ''summary'',
     ADD COLUMN detail_fetched_at DATETIME(6) NULL,
     ADD COLUMN episodes_fetched_at DATETIME(6) NULL,
     ADD COLUMN series_id BIGINT NULL,
     ADD COLUMN updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6)',
  'DO 0'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

CREATE TABLE IF NOT EXISTS candidate_episode (
  id BIGINT NOT NULL AUTO_INCREMENT,
  candidate_id BIGINT NOT NULL,
  external_id VARCHAR(64) NOT NULL,
  episode_no INT NOT NULL,
  title VARCHAR(255) NOT NULL DEFAULT '',
  created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  PRIMARY KEY (id),
  UNIQUE KEY uk_candidate_episode (candidate_id, external_id),
  UNIQUE KEY uk_candidate_episode_no (candidate_id, episode_no)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

SET @ddl = IF(
  (SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = 'content_series' AND column_name = 'current_draft_revision_id') = 0,
  'ALTER TABLE content_series
     ADD COLUMN current_draft_revision_id BIGINT NULL,
     ADD COLUMN published_revision_id BIGINT NULL,
     ADD COLUMN version INT NOT NULL DEFAULT 1',
  'DO 0'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

CREATE TABLE IF NOT EXISTS content_revision (
  id BIGINT NOT NULL AUTO_INCREMENT,
  series_id BIGINT NOT NULL,
  revision_no INT NOT NULL,
  state VARCHAR(16) NOT NULL DEFAULT 'draft',
  version INT NOT NULL DEFAULT 1,
  title VARCHAR(255) NOT NULL DEFAULT '',
  description TEXT NOT NULL,
  cover_url VARCHAR(512) NOT NULL DEFAULT '',
  tags JSON NULL,
  created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
  PRIMARY KEY (id),
  UNIQUE KEY uk_series_revision (series_id, revision_no)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS revision_episode (
  id BIGINT NOT NULL AUTO_INCREMENT,
  revision_id BIGINT NOT NULL,
  external_id VARCHAR(64) NOT NULL,
  episode_no INT NOT NULL,
  selected TINYINT(1) NOT NULL DEFAULT 0,
  created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  PRIMARY KEY (id),
  UNIQUE KEY uk_revision_episode (revision_id, external_id),
  UNIQUE KEY uk_revision_episode_no (revision_id, episode_no)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- Backfill a draft revision for series created by the previous slice.
INSERT INTO content_revision (series_id, revision_no, state, version, title, description, cover_url, tags, created_at, updated_at)
SELECT s.id, s.revision, 'draft', 1, s.title, s.description, s.cover_url, JSON_ARRAY(), s.created_at, s.updated_at
FROM content_series s
WHERE s.current_draft_revision_id IS NULL
  AND NOT EXISTS (SELECT 1 FROM content_revision r WHERE r.series_id = s.id);

UPDATE content_series s
JOIN content_revision r ON r.series_id = s.id AND r.revision_no = s.revision
SET s.current_draft_revision_id = r.id
WHERE s.current_draft_revision_id IS NULL;

INSERT INTO revision_episode (revision_id, external_id, episode_no, selected, created_at)
SELECT s.current_draft_revision_id, e.external_id, e.episode_no, e.selected, e.created_at
FROM series_episode e
JOIN content_series s ON s.id = e.series_id
WHERE s.current_draft_revision_id IS NOT NULL
  AND NOT EXISTS (
    SELECT 1 FROM revision_episode re WHERE re.revision_id = s.current_draft_revision_id AND re.external_id = e.external_id
  );

-- Keep the official Todo sample available without Ent auto-migration.
CREATE TABLE IF NOT EXISTS todos (
  id char(36) NOT NULL,
  created_at timestamp NOT NULL,
  updated_at timestamp NOT NULL,
  title varchar(255) NOT NULL DEFAULT '',
  content varchar(255) NOT NULL DEFAULT '',
  completed bool NOT NULL DEFAULT false,
  status int NOT NULL DEFAULT 1,
  PRIMARY KEY (id),
  KEY todo_updated_at (updated_at),
  KEY todo_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

INSERT INTO ingestion_plan (code, name, source_code, channel, page_start, page_end, request_budget, item_budget, fetch_details, fetch_episodes, enabled, version)
SELECT 'hongguo-hot-drama', '红果热播榜', 'hongguo', 'hot-drama', 1, 1, 80, 200, 1, 1, 1, 1
WHERE NOT EXISTS (SELECT 1 FROM ingestion_plan WHERE code = 'hongguo-hot-drama');
