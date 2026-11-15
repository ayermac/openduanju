-- Budget/rate bookkeeping for persisted runs.
SET @ddl = IF(
  (SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = 'ingestion_plan' AND column_name = 'rate_per_minute') = 0,
  'ALTER TABLE ingestion_plan ADD COLUMN rate_per_minute INT NOT NULL DEFAULT 30',
  'DO 0'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

SET @ddl = IF(
  (SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = 'ingestion_run' AND column_name = 'rate_per_minute') = 0,
  'ALTER TABLE ingestion_run ADD COLUMN rate_per_minute INT NOT NULL DEFAULT 30',
  'DO 0'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

SET @ddl = IF(
  (SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = 'ingestion_run' AND column_name = 'rate_window_start') = 0,
  'ALTER TABLE ingestion_run ADD COLUMN rate_window_start DATETIME(6) NULL',
  'DO 0'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

SET @ddl = IF(
  (SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = 'ingestion_run' AND column_name = 'rate_window_count') = 0,
  'ALTER TABLE ingestion_run ADD COLUMN rate_window_count INT NOT NULL DEFAULT 0',
  'DO 0'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

UPDATE ingestion_plan SET rate_per_minute = 30 WHERE rate_per_minute <= 0;
UPDATE ingestion_run SET rate_per_minute = 30 WHERE rate_per_minute <= 0;
