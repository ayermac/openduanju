-- 0013_review_checklist.sql
--
-- The checklist a reviewer must confirm before an approval. The row stores the
-- snapshot of what was ticked, so a later change to the form does not rewrite
-- an old decision. An approval without every item (and at least one loaded
-- episode) is refused by the domain layer.

ALTER TABLE `review_decision`
  ADD COLUMN `checklist` VARCHAR(512) NOT NULL DEFAULT '' COMMENT '通过时的检查项快照';
