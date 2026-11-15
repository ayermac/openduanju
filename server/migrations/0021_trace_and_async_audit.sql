-- 0021_trace_and_async_audit.sql
--
-- The trace identity has to survive the request. An ingestion run and a media
-- batch are queued by a request and executed by a worker, possibly after a
-- restart - by then the request context is gone. Storing the identity on the
-- row keeps the task, its logs and its audit event reachable from the page the
-- operator reported the error on.
--
-- The column is empty for the rows that were created before this migration.
-- An empty value means "no identity was recorded", not "no operation": the
-- rows stay readable, they simply cannot be joined to a request.
--
-- No payload, credential or signed address is stored here; the identity is a
-- random string the request carried.

ALTER TABLE ingestion_run
  ADD COLUMN trace_id VARCHAR(64) NOT NULL DEFAULT '' AFTER error_message,
  ADD KEY idx_ingestion_run_trace (trace_id);

ALTER TABLE media_batch
  ADD COLUMN trace_id VARCHAR(64) NOT NULL DEFAULT '' AFTER created_by,
  ADD KEY idx_media_batch_trace (trace_id);

-- A task is claimed on its own, so it carries the identity itself: the worker
-- reads the row after a restart and logs the stage it resumes with.
ALTER TABLE media_item
  ADD COLUMN trace_id VARCHAR(64) NOT NULL DEFAULT '' AFTER episode_no;

-- The audit list is read by object, by action, by operator and by trace. The
-- object filter is the common one, so it gets its own ordering key.
ALTER TABLE audit_event
  ADD KEY idx_audit_action (action, created_at),
  ADD KEY idx_audit_actor_name (actor_name, created_at);
