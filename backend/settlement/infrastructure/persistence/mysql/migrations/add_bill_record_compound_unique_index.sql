-- +migrate Up
CREATE UNIQUE INDEX IF NOT EXISTS idx_bill_record_round_type_user ON bill_record(round_trace_id, bill_type, user_id);

-- +migrate Down
DROP INDEX IF EXISTS idx_bill_record_round_type_user ON bill_record;
