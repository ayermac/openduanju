-- Audit of manual budget extensions; consumed counters stay on the run.
CREATE TABLE IF NOT EXISTS ingestion_run_budget (
  id BIGINT NOT NULL AUTO_INCREMENT,
  run_id BIGINT NOT NULL,
  additional_requests INT NOT NULL DEFAULT 0,
  additional_items INT NOT NULL DEFAULT 0,
  reason VARCHAR(255) NOT NULL DEFAULT '',
  created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  PRIMARY KEY (id),
  KEY ingestion_run_budget_run_id (run_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
