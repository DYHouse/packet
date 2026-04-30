#!/bin/bash

MERCHANT_ID="10001"
MERCHANT_SECRET="aca5d11a-e481-4163-9505-194564558ae3"
BASE_URL="http://43.135.35.31"

ts=$(date +%s)

echo "=== Step 1: Get User Token from Admin Service ==="
body='{"game_code":"packet","user_id":"test_user_ws","currency":"USD","lang":"en","username":"TestWSUser","client_ip":"127.0.0.1","version":"1.1"}'
sign_str="${body}{\"mid\":\"${MERCHANT_ID}\",\"ts\":\"${ts}\"}"
sign=$(echo -n "$sign_str" | openssl dgst -sha256 -hmac "$MERCHANT_SECRET" | awk '{print $2}')

response=$(curl -s -X POST "${BASE_URL}/game/start?mid=${MERCHANT_ID}&ts=${ts}&sign=${sign}" \
  -H "Content-Type: application/json" \
  -d "$body")

echo "Response: $response"

user_token=$(echo $response | jq -r '.data.user_token')

if [ "$user_token" == "null" ] || [ -z "$user_token" ]; then
    echo "❌ Failed to get user token"
    exit 1
fi

echo "✅ Got user token: ${user_token:0:50}..."
echo ""

echo "=== Step 2: Test WebSocket Connection ==="
echo "ws://${BASE_URL#http://}/ws?token=${user_token}&device_id=device_001&platform=web"
echo ""
echo "You can use this URL to test WebSocket connection with any WebSocket client"
echo ""
echo "Example using wscat:"
echo "wscat -c 'ws://43.135.35.31/ws?token=${user_token}&device_id=device_001&platform=web'"
