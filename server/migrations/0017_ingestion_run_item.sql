-- 0017_ingestion_run_item.sql
--
-- The rows one collection run produced. The relation is stored, not derived
-- from the candidate snapshot: a later run overwrites the candidate's
-- last_run_id, and the old run must keep its own rows and results.
--
-- Every attempt is one row, so a failed attempt stays visible after a
-- successful retry.

CREATE TABLE IF NOT EXISTS ingestion_run_item (
  id BIGINT NOT NULL AUTO_INCREMENT,
  run_id BIGINT NOT NULL,
  candidate_id BIGINT NOT NULL,
  candidate_external_id VARCHAR(64) NOT NULL DEFAULT '',
  -- stage is the step of the run: summary, detail or episodes.
  stage VARCHAR(32) NOT NULL DEFAULT '',
  -- result is success or failed.
  result VARCHAR(16) NOT NULL DEFAULT 'success',
  error_code VARCHAR(64) NOT NULL DEFAULT '',
  error_message VARCHAR(512) NOT NULL DEFAULT '',
  attempt INT NOT NULL DEFAULT 1,
  created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  PRIMARY KEY (id),
  KEY idx_ingestion_run_item_run (run_id, id),
  KEY idx_ingestion_run_item_candidate (candidate_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
