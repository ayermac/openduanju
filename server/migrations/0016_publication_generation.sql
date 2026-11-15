-- 0016_publication_generation.sql
--
-- A publication carries a generation. Every publish bumps it, so a playback
-- session can tell that it belongs to an older release: the old token stops
-- working and is never restored by a re-publication.
--
-- The category is frozen with the publication, so the public list filters on
-- the label that was current at publish time instead of a later edit.

ALTER TABLE `publication`
  ADD COLUMN `generation` INT NOT NULL DEFAULT 1 COMMENT '发布代次，每次发布递增',
  ADD COLUMN `category` VARCHAR(64) NOT NULL DEFAULT '' COMMENT '发布时冻结的分类';

ALTER TABLE `playback_session`
  ADD COLUMN `publication_generation` INT NOT NULL DEFAULT 1 COMMENT '会话绑定的发布代次';
