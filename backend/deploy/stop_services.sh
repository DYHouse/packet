#!/bin/bash

cd /cosmos/backend

if [ -f logs/game.pid ]; then
    GAME_PID=$(cat logs/game.pid)
    if kill -0 $GAME_PID 2>/dev/null; then
        echo "Stopping game service (PID: $GAME_PID)..."
        kill $GAME_PID
        rm logs/game.pid
        echo "Game service stopped"
    else
        echo "Game service is not running"
        rm logs/game.pid
    fi
else
    echo "Game service PID file not found"
fi

if [ -f logs/gateway.pid ]; then
    GATEWAY_PID=$(cat logs/gateway.pid)
    if kill -0 $GATEWAY_PID 2>/dev/null; then
        echo "Stopping gateway service (PID: $GATEWAY_PID)..."
        kill $GATEWAY_PID
        rm logs/gateway.pid
        echo "Gateway service stopped"
    else
        echo "Gateway service is not running"
        rm logs/gateway.pid
    fi
else
    echo "Gateway service PID file not found"
fi
