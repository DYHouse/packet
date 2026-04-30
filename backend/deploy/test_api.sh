#!/bin/bash

MERCHANT_ID="10001"
MERCHANT_SECRET="aca5d11a-e481-4163-9505-194564558ae3"
BASE_URL="http://43.135.35.31:8090"

ts=$(date +%s)

json_str="{}"
sign_str="${json_str}{\"mid\":\"${MERCHANT_ID}\",\"ts\":\"${ts}\"}"
sign=$(echo -n "$sign_str" | openssl dgst -sha256 -hmac "$MERCHANT_SECRET" | awk '{print $2}')

echo "Testing Game List API..."
echo "URL: ${BASE_URL}/game/list?mid=${MERCHANT_ID}&ts=${ts}&sign=${sign}"
curl -X GET "${BASE_URL}/game/list?mid=${MERCHANT_ID}&ts=${ts}&sign=${sign}"

echo -e "\n\nTesting Game Start API..."
body='{"game_code":"packet","user_id":"test_user_123","currency":"USD","lang":"en","username":"TestUser","client_ip":"127.0.0.1","version":"1.1"}'
sign_str="${body}{\"mid\":\"${MERCHANT_ID}\",\"ts\":\"${ts}\"}"
sign=$(echo -n "$sign_str" | openssl dgst -sha256 -hmac "$MERCHANT_SECRET" | awk '{print $2}')

echo "URL: ${BASE_URL}/game/start?mid=${MERCHANT_ID}&ts=${ts}&sign=${sign}"
curl -X POST "${BASE_URL}/game/start?mid=${MERCHANT_ID}&ts=${ts}&sign=${sign}" \
  -H "Content-Type: application/json" \
  -d "$body"
