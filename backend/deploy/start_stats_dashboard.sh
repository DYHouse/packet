#!/bin/bash

cd /cosmos/backend

export PATH=$PATH:/usr/local/go/bin

mkdir -p bin logs

echo "Building stats service..."
go build -o bin/stats cmd/stats/main.go

echo "Starting stats service..."
nohup ./bin/stats > logs/stats.log 2>&1 &
STATS_PID=$!
echo $STATS_PID > logs/stats.pid
echo "Stats service started with PID: $STATS_PID (port 9090)"

echo "Installing frontend dependencies..."
cd /cosmos/dashboard
if [ ! -d "node_modules" ]; then
  npm install
fi

echo "Starting frontend (vite dev server)..."
nohup npx vite --host 0.0.0.0 > /cosmos/backend/logs/dashboard.log 2>&1 &
DASHBOARD_PID=$!
echo $DASHBOARD_PID > /cosmos/backend/logs/dashboard.pid
echo "Frontend started with PID: $DASHBOARD_PID (port 3000)"

echo ""
echo "All services started!"
echo "  Stats API:   http://localhost:8081"
echo "  Dashboard:   http://localhost:3000"
echo ""
echo "Logs: /cosmos/backend/logs/stats.log, /cosmos/backend/logs/dashboard.log"
