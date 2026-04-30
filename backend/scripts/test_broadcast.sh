#!/bin/bash

echo "=== Redis Pub/Sub 广播测试 ==="
echo ""

echo "1. 检查活跃的Pub/Sub Channel:"
docker exec -i cashparty-redis redis-cli PUBSUB CHANNELS
echo ""

echo "2. 检查 cashparty:gateway:broadcast 的订阅者数量:"
docker exec -i cashparty-redis redis-cli PUBSUB NUMSUB cashparty:gateway:broadcast
echo ""

echo "3. 发送测试广播消息:"
docker exec -i cashparty-redis redis-cli PUBLISH cashparty:gateway:broadcast '{"target_type":"room","target_id":"test_room","event":"test_event","data":{"message":"test broadcast"},"exclude_id":""}'
echo ""

echo "4. 检查Redis中的Key（注意：Pub/Sub Channel不会作为Key存储）:"
docker exec -i cashparty-redis redis-cli KEYS "cashparty:*" | head -20
echo ""

echo "=== 测试完成 ==="
echo ""
echo "说明："
echo "- Pub/Sub Channel不会出现在KEYS命令结果中"
echo "- 消息是实时发送的，不存储在Redis中"
echo "- 使用 PUBSUB CHANNELS 和 PUBSUB NUMSUB 来查看Channel状态"
