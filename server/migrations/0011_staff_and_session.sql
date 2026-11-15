-- 0011_staff_and_session.sql
--
-- Real staff identities and revocable sessions. The shared bearer token is
-- removed: every admin call carries a session cookie, and every actor field
-- is taken from the authenticated staff row.
--
-- The password hash is PBKDF2-SHA256 with a per-row salt; no default account
-- and no built-in password exist. The first administrator is created by the
-- one-shot openduanju-staff-init command.

CREATE TABLE IF NOT EXISTS staff (
  id BIGINT NOT NULL AUTO_INCREMENT,
  username VARCHAR(64) NOT NULL,
  display_name VARCHAR(128) NOT NULL DEFAULT '',
  role VARCHAR(16) NOT NULL DEFAULT 'operator',
  enabled TINYINT(1) NOT NULL DEFAULT 1,
  password_hash VARCHAR(255) NOT NULL,
  -- password_version and version let an update fence stale editors.
  password_version INT NOT NULL DEFAULT 1,
  version INT NOT NULL DEFAULT 1,
  last_login_at DATETIME(6) NULL,
  created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
  PRIMARY KEY (id),
  UNIQUE KEY uk_staff_username (username)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS staff_session (
  id BIGINT NOT NULL AUTO_INCREMENT,
  staff_id BIGINT NOT NULL,
  -- token_hash is the sha256 of the cookie value; the raw token is never
  -- stored, so a database read cannot be replayed as a session.
  token_hash CHAR(64) NOT NULL,
  csrf_token VARCHAR(64) NOT NULL,
  expires_at DATETIME(6) NOT NULL,
  revoked_at DATETIME(6) NULL,
  created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  PRIMARY KEY (id),
  UNIQUE KEY uk_staff_session_token (token_hash),
  KEY idx_staff_session_staff (staff_id, revoked_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
