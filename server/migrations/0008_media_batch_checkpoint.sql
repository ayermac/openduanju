-- 0008_media_batch_checkpoint.sql
--
-- The persistent media worker needs the checkpoint of a task to be usable
-- for a resume: the reusable local artifacts of the download and the
-- processing, and the identity of an already uploaded object. Without them a
-- crashed task always restarts from the resolver and re-downloads and
-- re-uploads the whole episode.
--
-- The columns are additive: an existing row keeps an empty artifact
-- reference, which the worker reads as "no reusable artifact".

ALTER TABLE `media_item`
  ADD COLUMN `input_artifact_path` VARCHAR(512) NOT NULL DEFAULT '' COMMENT '可复用的本地输入文件路径',
  ADD COLUMN `input_artifact_sha256` VARCHAR(64) NOT NULL DEFAULT '' COMMENT '输入文件的sha256',
  ADD COLUMN `output_artifact_path` VARCHAR(512) NOT NULL DEFAULT '' COMMENT '可复用的本地成品文件路径',
  ADD COLUMN `output_artifact_sha256` VARCHAR(64) NOT NULL DEFAULT '' COMMENT '成品的sha256',
  ADD COLUMN `output_artifact_size` BIGINT NOT NULL DEFAULT 0 COMMENT '成品的字节数';
