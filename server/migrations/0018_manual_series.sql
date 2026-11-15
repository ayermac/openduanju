-- 0018_manual_series.sql
--
-- A manual work has no provider and therefore no external identifier: the
-- operator declares the title, the description and the original episode
-- numbers, and the work goes through the same draft, upload, processing,
-- review and publication path as an imported one.
--
-- Both identifiers become nullable. MySQL treats NULLs as distinct in a unique
-- index, so uk_series and uk_revision_episode keep rejecting a repeated
-- provider identifier while any number of manual rows can exist with no
-- identifier at all. No row is rewritten: an imported work keeps its values.
--
-- request_id is the idempotency key of the creation form. It is optional for
-- the import path and unique when present, so a double click or a retried
-- submission returns the series that already exists instead of a second one.
-- request_fingerprint records what the key was used for: the same key with a
-- different payload is a conflict, not a silent reuse.
--
-- Every statement is guarded, so applying this file to a database that already
-- carries the column or the index changes nothing.

SET @ddl = IF(
  (SELECT IS_NULLABLE FROM information_schema.columns
     WHERE table_schema = DATABASE() AND table_name = 'content_series' AND column_name = 'external_id') = 'NO',
  'ALTER TABLE content_series MODIFY COLUMN external_id VARCHAR(64) NULL',
  'DO 0'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

SET @ddl = IF(
  (SELECT COUNT(*) FROM information_schema.columns
     WHERE table_schema = DATABASE() AND table_name = 'content_series' AND column_name = 'request_id') = 0,
  'ALTER TABLE content_series ADD COLUMN request_id VARCHAR(64) NULL, ADD COLUMN request_fingerprint CHAR(64) NOT NULL DEFAULT ''''',
  'DO 0'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

SET @ddl = IF(
  (SELECT COUNT(*) FROM information_schema.statistics
     WHERE table_schema = DATABASE() AND table_name = 'content_series' AND index_name = 'uk_series_request') = 0,
  'ALTER TABLE content_series ADD UNIQUE KEY uk_series_request (request_id)',
  'DO 0'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

SET @ddl = IF(
  (SELECT IS_NULLABLE FROM information_schema.columns
     WHERE table_schema = DATABASE() AND table_name = 'revision_episode' AND column_name = 'external_id') = 'NO',
  'ALTER TABLE revision_episode MODIFY COLUMN external_id VARCHAR(64) NULL',
  'DO 0'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
