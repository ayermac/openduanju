-- 0010_authorization_grant.sql
--
-- Series-level authorization grants. One grant covers exactly one series;
-- there is no source-level inheritance, no merging and no fallback. A grant
-- is append-only: a wrong permission, scope or validity window is corrected
-- by creating a new grant, and an old grant can only be revoked.
--
-- The three permission bits default to 0: a grant that forgot a bit must not
-- silently allow processing, display or playback.
--
-- valid_until NULL means the system has no end date; it is not a statement of
-- a perpetual license and the UI must not present it as one.

CREATE TABLE IF NOT EXISTS authorization_grant (
  id BIGINT NOT NULL AUTO_INCREMENT,
  series_id BIGINT NOT NULL,
  reference VARCHAR(255) NOT NULL DEFAULT '',
  allow_process TINYINT(1) NOT NULL DEFAULT 0,
  allow_display TINYINT(1) NOT NULL DEFAULT 0,
  allow_playback TINYINT(1) NOT NULL DEFAULT 0,
  valid_from DATETIME(6) NOT NULL,
  valid_until DATETIME(6) NULL,
  status VARCHAR(16) NOT NULL DEFAULT 'active',
  version INT NOT NULL DEFAULT 1,
  confirmed_by VARCHAR(64) NOT NULL DEFAULT '',
  confirmed_at DATETIME(6) NOT NULL,
  revoked_by VARCHAR(64) NOT NULL DEFAULT '',
  revoked_at DATETIME(6) NULL,
  revoke_reason VARCHAR(512) NOT NULL DEFAULT '',
  created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
  PRIMARY KEY (id),
  KEY idx_authorization_series (series_id, status),
  KEY idx_authorization_validity (status, valid_until)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- The revision freezes the grant it was submitted with. The hash covers the
-- permission bits, the reference and the validity window, so a later change
-- of the audit trail is detectable without copying the authorization
-- material itself.
ALTER TABLE `content_revision`
  ADD COLUMN `authorization_grant_id` BIGINT NOT NULL DEFAULT 0 COMMENT '冻结的授权依据ID',
  ADD COLUMN `authorization_version` INT NOT NULL DEFAULT 0 COMMENT '冻结的依据版本',
  ADD COLUMN `authorization_hash` VARCHAR(64) NOT NULL DEFAULT '' COMMENT '冻结依据关键字段指纹';
