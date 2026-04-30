#!/bin/bash

NACOS_ADDR="http://127.0.0.1:8848"
NAMESPACE=""
GROUP="DEFAULT_GROUP"
USERNAME="nacos"
PASSWORD="nacos"

# 获取access token
TOKEN=$(curl -s -X POST "${NACOS_ADDR}/nacos/v1/auth/login" -d "username=${USERNAME}&password=${PASSWORD}" | grep -o '"accessToken":"[^"]*"' | cut -d'"' -f4)

if [ -z "$TOKEN" ]; then
    echo "Failed to get access token"
    exit 1
fi

echo "Access token obtained: ${TOKEN:0:20}..."

# 上传game-service配置
echo "Uploading game-service.yaml..."
curl -X POST "${NACOS_ADDR}/nacos/v1/cs/configs" \
    -d "dataId=game-service.yaml" \
    -d "group=${GROUP}" \
    -d "tenant=${NAMESPACE}" \
    -d "accessToken=${TOKEN}" \
    --data-urlencode "content@/cosmos/backend/config/game.yaml"

echo -e "\n"

# 上传gateway-service配置
echo "Uploading gateway-service.yaml..."
curl -X POST "${NACOS_ADDR}/nacos/v1/cs/configs" \
    -d "dataId=gateway-service.yaml" \
    -d "group=${GROUP}" \
    -d "tenant=${NAMESPACE}" \
    -d "accessToken=${TOKEN}" \
    --data-urlencode "content@/cosmos/backend/config/gateway.yaml"

echo -e "\n"

# 上传gateway-router配置
echo "Uploading gateway-router.yaml..."
curl -X POST "${NACOS_ADDR}/nacos/v1/cs/configs" \
    -d "dataId=gateway-router.yaml" \
    -d "group=${GROUP}" \
    -d "tenant=${NAMESPACE}" \
    -d "accessToken=${TOKEN}" \
    --data-urlencode "content@/cosmos/backend/config/gateway-router.yaml"

echo -e "\n"

# 上传algorithm配置
echo "Uploading algorithm.yaml..."
curl -X POST "${NACOS_ADDR}/nacos/v1/cs/configs" \
    -d "dataId=algorithm.yaml" \
    -d "group=${GROUP}" \
    -d "tenant=${NAMESPACE}" \
    -d "accessToken=${TOKEN}" \
    --data-urlencode "content@/cosmos/backend/config/algorithm.yaml"

echo -e "\n"
echo "All configurations uploaded successfully!"
