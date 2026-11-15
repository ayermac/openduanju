-- 0020_audit_event.sql
--
-- One row per write the console performed: who did it, to which resource, with
-- which version, what came of it, and the trace it belongs to. The row holds
-- no payload, no credential and no signed address, so it can be kept and read
-- without becoming a copy of the data it describes.
--
-- The trace identity is the one the request carried. It is stored as a plain
-- column so an operator can follow one operation across the audit list, the
-- task rows and the error reports.

CREATE TABLE IF NOT EXISTS audit_event (
  id BIGINT NOT NULL AUTO_INCREMENT,
  trace_id VARCHAR(64) NOT NULL DEFAULT '',
  actor_id BIGINT NOT NULL DEFAULT 0,
  actor_name VARCHAR(64) NOT NULL DEFAULT '',
  action VARCHAR(64) NOT NULL,
  resource_type VARCHAR(32) NOT NULL,
  resource_id VARCHAR(64) NOT NULL DEFAULT '',
  resource_version INT NOT NULL DEFAULT 0,
  -- result is success or failure; a failed operation is never a success.
  result VARCHAR(16) NOT NULL DEFAULT 'success',
  -- reason keeps the stable failure reason, never a raw error body.
  reason VARCHAR(128) NOT NULL DEFAULT '',
  created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  PRIMARY KEY (id),
  KEY idx_audit_resource (resource_type, resource_id),
  KEY idx_audit_actor (actor_id),
  KEY idx_audit_trace (trace_id),
  KEY idx_audit_created (created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
