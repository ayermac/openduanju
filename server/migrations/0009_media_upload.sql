-- 0009_media_upload.sql
--
-- MEDIA-002 M3-B: upload sessions for manual episode input and the cover.
--
-- An upload session is the only way a file enters the pipeline from the
-- admin UI. It binds one revision (and optionally one episode), a fixed
-- staging object key and a short-lived upload authorization. Completing a
-- session registers a *trusted input*: the video still has to go through
-- validate/process/upload/verify, and a cover only becomes ready after the
-- server confirmed the stored object is a real image.
--
-- The staging key is unique per session: a renewed authorization signs the
-- same key, so chunked uploads continue on one object identity instead of
-- creating a second binding.

CREATE TABLE IF NOT EXISTS upload_session (
  id BIGINT NOT NULL AUTO_INCREMENT,
  revision_id BIGINT NOT NULL,
  episode_id BIGINT NOT NULL DEFAULT 0,
  kind VARCHAR(16) NOT NULL DEFAULT 'episode_input',
  status VARCHAR(16) NOT NULL DEFAULT 'pending',
  version INT NOT NULL DEFAULT 1,
  provider VARCHAR(16) NOT NULL DEFAULT '',
  bucket VARCHAR(128) NOT NULL DEFAULT '',
  region VARCHAR(64) NOT NULL DEFAULT '',
  staging_key VARCHAR(512) NOT NULL,
  object_key VARCHAR(512) NOT NULL DEFAULT '',
  object_version VARCHAR(128) NOT NULL DEFAULT '',
  filename VARCHAR(255) NOT NULL DEFAULT '',
  content_type VARCHAR(128) NOT NULL DEFAULT '',
  size_bytes BIGINT NOT NULL DEFAULT 0,
  expected_sha256 VARCHAR(64) NOT NULL DEFAULT '',
  sha256 VARCHAR(64) NOT NULL DEFAULT '',
  width INT NOT NULL DEFAULT 0,
  height INT NOT NULL DEFAULT 0,
  asset_id BIGINT NOT NULL DEFAULT 0,
  created_by VARCHAR(64) NOT NULL DEFAULT '',
  revision_version INT NOT NULL DEFAULT 0,
  expires_at DATETIME(6) NOT NULL,
  completed_at DATETIME(6) NULL,
  created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
  PRIMARY KEY (id),
  UNIQUE KEY uk_upload_staging (provider, bucket, region, staging_key(191)),
  KEY idx_upload_revision (revision_id, kind, status),
  KEY idx_upload_expiry (status, expires_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- A revision episode carries the input a batch will freeze. The values are
-- empty for an episode that was never given a local file, so the SDK path
-- stays the default.
ALTER TABLE `revision_episode`
  ADD COLUMN `input_kind` VARCHAR(16) NOT NULL DEFAULT 'sdk' COMMENT '输入类型：sdk/manual',
  ADD COLUMN `input_ref` BIGINT NOT NULL DEFAULT 0 COMMENT '人工输入的上传会话ID',
  ADD COLUMN `input_state` VARCHAR(16) NOT NULL DEFAULT '' COMMENT '人工输入状态：ready为已确认',
  ADD COLUMN `asset_id` BIGINT NOT NULL DEFAULT 0 COMMENT '该分集已就绪的素材ID',
  ADD COLUMN `asset_state` VARCHAR(16) NOT NULL DEFAULT '' COMMENT '该分集素材状态';

-- The cover is bound to the revision, not to a batch: a cover may be
-- uploaded while the revision is still a draft.
ALTER TABLE `content_revision`
  ADD COLUMN `cover_asset_id` BIGINT NOT NULL DEFAULT 0 COMMENT '已固化的封面素材ID';

-- A task may be fed by a confirmed upload instead of the SDK resolver.
ALTER TABLE `media_item`
  ADD COLUMN `input_upload_id` BIGINT NOT NULL DEFAULT 0 COMMENT '人工输入的上传会话ID';

-- One asset table serves episode media and covers; the kind keeps the two
-- apart without mixing a cover into the episode counters.
ALTER TABLE `media_asset`
  ADD COLUMN `kind` VARCHAR(16) NOT NULL DEFAULT 'episode' COMMENT '素材类型：episode/cover';
