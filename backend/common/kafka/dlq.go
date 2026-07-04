package kafka

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// DLQEnvelope 是投递到死信队列的消息包装格式。
type DLQEnvelope struct {
	OriginalTopic     string          `json:"original_topic"`
	OriginalPartition int             `json:"original_partition"`
	OriginalOffset    int64           `json:"original_offset"`
	OriginalKey       []byte          `json:"original_key,omitempty"`
	OriginalValue     json.RawMessage `json:"original_value"`
	Error             string          `json:"error"`
	Timestamp         int64           `json:"timestamp"`
}

// sendToDLQ 将原始消息与处理错误包装为 DLQEnvelope 投递到死信队列。
func sendToDLQ(ctx context.Context, producer *Producer, dlqTopic string, msg Message, handlerErr error) error {
	envelope := DLQEnvelope{
		OriginalTopic:     msg.Topic,
		OriginalPartition: msg.Partition,
		OriginalOffset:    msg.Offset,
		OriginalKey:       msg.Key,
		OriginalValue:     msg.Value,
		Error:             handlerErr.Error(),
		Timestamp:         time.Now().UnixMilli(),
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("marshal DLQ envelope failed: %w", err)
	}
	if err := producer.Send(ctx, dlqTopic, msg.Key, payload); err != nil {
		return fmt.Errorf("send to DLQ failed: %w", err)
	}
	return nil
}
