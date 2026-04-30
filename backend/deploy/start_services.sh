#!/bin/bash

cd /cosmos/backend

export PATH=$PATH:/usr/local/go/bin

echo "Building game service..."
go build -o bin/game cmd/game/main.go

echo "Building gateway service..."
go build -o bin/gateway cmd/gateway/main.go

echo "Starting game service..."
nohup ./bin/game > logs/game.log 2>&1 &
GAME_PID=$!
echo $GAME_PID > logs/game.pid
echo "Game service started with PID: $GAME_PID"

echo "Starting gateway service..."
nohup ./bin/gateway > logs/gateway.log 2>&1 &
GATEWAY_PID=$!
echo $GATEWAY_PID > logs/gateway.pid
echo "Gateway service started with PID: $GATEWAY_PID"

echo "All services started!"
echo "Game PID: $GAME_PID"
echo "Gateway PID: $GATEWAY_PID"
