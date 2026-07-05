-- 修复 BillRecord 复合唯一索引：从单字段唯一改为三字段复合唯一
-- 背景：原 GORM 标签只在 RoundTraceID 上声明 uniqueIndex，导致 AutoMigrate 创建单字段唯一索引，
-- 阻止同一 round_trace_id 的多种 bill_type 账单共存。改为 (round_trace_id, bill_type, user_id) 复合唯一索引。
-- 关联 spec: fix-refund-bill-p0-residual (P0-3)

ALTER TABLE bill_record
  DROP INDEX IF EXISTS idx_bill_record_round_type_user,
  ADD UNIQUE INDEX idx_round_trace_bill_user (round_trace_id, bill_type, user_id);
