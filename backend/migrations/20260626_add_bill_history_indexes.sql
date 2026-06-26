-- 玩家历史记录对账数据修正：为 bill_record 聚合查询新增索引
-- 详见 docs/PLAYER_HISTORY_BILL_REFACTOR_PLAN.md
-- 历史查询三个接口（列表/详情/统计概览）改为基于 bill_record 聚合，需要以下索引支持性能

-- bill_record 表：按 user_id + status 聚合 session_id（列表查询与累计统计）
ALTER TABLE bill_record ADD INDEX idx_user_status_session (user_id, status, session_id);

-- bill_record 表：按 session_id + user_id + bill_type 过滤（单局详情聚合）
ALTER TABLE bill_record ADD INDEX idx_session_user_type (session_id, user_id, bill_type, status);

-- rounds 表：按 session_id + sender_id 查询玩家发包回合（详情页 MySend 填充）
ALTER TABLE rounds ADD INDEX idx_session_sender (session_id, sender_id, round_no);

-- 验证索引命中（运维执行后用以下 EXPLAIN 语句验证）：
-- EXPLAIN SELECT session_id, SUM(CASE WHEN bill_type = 3 THEN amount ELSE 0 END) FROM bill_record WHERE user_id = 1001 AND status = 1 GROUP BY session_id;
-- 期望命中 idx_user_status_session
--
-- EXPLAIN SELECT SUM(amount) FROM bill_record WHERE session_id = 100 AND user_id = 1001 AND status = 1 AND bill_type != 12;
-- 期望命中 idx_session_user_type
--
-- EXPLAIN SELECT * FROM rounds WHERE session_id = 100 AND sender_id = 1001 AND sender_type IN ('player','system_resume') ORDER BY round_no ASC;
-- 期望命中 idx_session_sender
