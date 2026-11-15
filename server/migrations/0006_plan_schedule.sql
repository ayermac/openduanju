-- Cron schedule for collection plans; an empty cron keeps the plan manual.
SET @ddl = IF(
  (SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = 'ingestion_plan' AND column_name = 'cron') = 0,
  'ALTER TABLE ingestion_plan ADD COLUMN cron VARCHAR(64) NOT NULL DEFAULT ''''',
  'DO 0'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

SET @ddl = IF(
  (SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = 'ingestion_plan' AND column_name = 'timezone') = 0,
  'ALTER TABLE ingestion_plan ADD COLUMN timezone VARCHAR(64) NOT NULL DEFAULT ''Asia/Shanghai''',
  'DO 0'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

SET @ddl = IF(
  (SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = 'ingestion_plan' AND column_name = 'next_run_at') = 0,
  'ALTER TABLE ingestion_plan ADD COLUMN next_run_at DATETIME(6) NULL',
  'DO 0'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

SET @ddl = IF(
  (SELECT COUNT(*) FROM information_schema.statistics WHERE table_schema = DATABASE() AND table_name = 'ingestion_plan' AND index_name = 'idx_ingestion_plan_next_run_at') = 0,
  'CREATE INDEX idx_ingestion_plan_next_run_at ON ingestion_plan (next_run_at)',
  'DO 0'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

UPDATE ingestion_plan SET timezone = 'Asia/Shanghai' WHERE timezone = '';
UPDATE ingestion_plan SET next_run_at = NULL WHERE cron = '';
