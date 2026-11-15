-- 0012_review_and_throttle.sql
--
-- Login throttling and the review decision/publication records.
--
-- Throttling counts failures inside a sliding window. There is no lock flag:
-- a good password after the window works again, so an administrator can never
-- be locked out permanently.

CREATE TABLE IF NOT EXISTS login_attempt (
  id BIGINT NOT NULL AUTO_INCREMENT,
  username VARCHAR(64) NOT NULL,
  attempted_at DATETIME(6) NOT NULL,
  succeeded TINYINT(1) NOT NULL DEFAULT 0,
  PRIMARY KEY (id),
  KEY idx_login_attempt_window (username, attempted_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- One decision per review task. A row is never updated: a corrected outcome
-- means a new revision and a new review, not a rewritten history.
CREATE TABLE IF NOT EXISTS review_decision (
  id BIGINT NOT NULL AUTO_INCREMENT,
  review_task_id BIGINT NOT NULL,
  decision VARCHAR(16) NOT NULL,
  reason VARCHAR(1000) NOT NULL DEFAULT '',
  decided_by VARCHAR(64) NOT NULL,
  decided_at DATETIME(6) NOT NULL,
  task_version INT NOT NULL,
  -- The authorization the approval relied on, frozen with the decision.
  authorization_grant_id BIGINT NOT NULL DEFAULT 0,
  authorization_version INT NOT NULL DEFAULT 0,
  authorization_hash VARCHAR(64) NOT NULL DEFAULT '',
  created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  PRIMARY KEY (id),
  UNIQUE KEY uk_review_decision_task (review_task_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- The published revision of one series. The row freezes the revision and the
-- authorization it was approved with; a later draft never rewrites it.
CREATE TABLE IF NOT EXISTS publication (
  id BIGINT NOT NULL AUTO_INCREMENT,
  series_id BIGINT NOT NULL,
  revision_id BIGINT NOT NULL,
  status VARCHAR(16) NOT NULL DEFAULT 'published',
  published_by VARCHAR(64) NOT NULL,
  published_at DATETIME(6) NOT NULL,
  unpublished_by VARCHAR(64) NOT NULL DEFAULT '',
  unpublished_at DATETIME(6) NULL,
  authorization_grant_id BIGINT NOT NULL DEFAULT 0,
  authorization_version INT NOT NULL DEFAULT 0,
  authorization_hash VARCHAR(64) NOT NULL DEFAULT '',
  version INT NOT NULL DEFAULT 1,
  created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
  PRIMARY KEY (id),
  UNIQUE KEY uk_publication_revision (revision_id),
  KEY idx_publication_series (series_id, status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
