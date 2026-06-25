-- Task 7: 玩家历史游戏记录功能索引
-- 为支持玩家历史查询性能，在以下表新增索引（若不存在）
-- 详见 docs/PLAYER_HISTORY_DESIGN.md 第七章《数据库索引建议》

-- session_players 表：按用户查询历史参与的游戏会话
ALTER TABLE session_players ADD INDEX idx_user_joined (user_id, joined_at DESC);

-- round_grab_records 表：按会话+用户查询抢包记录
ALTER TABLE round_grab_records ADD INDEX idx_session_user (session_id, user_id, grabbed_at);

-- rounds 表：按会话查询回合列表
ALTER TABLE rounds ADD INDEX idx_session_roundno (session_id, round_no);

-- bill_record 表：按用户+会话查询流水（为 P2 功能预留）
ALTER TABLE bill_record ADD INDEX idx_user_session (user_id, session_id, created_at);
