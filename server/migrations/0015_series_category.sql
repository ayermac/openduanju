-- 0015_series_category.sql
--
-- A public category label. It is a presentation attribute of the series, not
-- a ranking signal: the public list only filters on it.

ALTER TABLE `content_series`
  ADD COLUMN `category` VARCHAR(64) NOT NULL DEFAULT '' COMMENT '公开分类标签';
