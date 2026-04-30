package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"time"

	"github.com/gorilla/websocket"
)

func main() {
	fmt.Println("=== WebSocket Connection Test ===")
	
	tokenData := map[string]string{
		"user_id":   "test_user_ws_001",
		"nickname":  "TestWSUser",
	}
	
	tokenJSON, _ := json.Marshal(tokenData)
	escaped := url.QueryEscape(string(tokenJSON))
	token := base64.StdEncoding.EncodeToString([]byte(escaped))
	
	fmt.Printf("Generated token: %s...\n\n", token[:50])
	
	wsURL := fmt.Sprintf("ws://43.135.35.31/ws?token=%s&device_id=device_001&platform=web", token)
	fmt.Printf("WebSocket URL: %s\n\n", wsURL)
	
	headers := http.Header{}
	headers.Set("Origin", "http://43.135.35.31")
	
	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}
	
	conn, _, err := dialer.Dial(wsURL, headers)
	if err != nil {
		log.Fatalf("❌ Failed to connect: %v", err)
	}
	defer conn.Close()
	
	fmt.Println("✅ WebSocket connected successfully!")
	fmt.Printf("Remote Address: %s\n\n", conn.RemoteAddr())
	
	authMsg := fmt.Sprintf(`{"cmd":"auth","request_id":"auth_001","data":{"token":"%s"},"timestamp":%d}`, token, time.Now().UnixMilli())
	err = conn.WriteMessage(websocket.TextMessage, []byte(authMsg))
	if err != nil {
		log.Fatalf("❌ Failed to send auth message: %v", err)
	}
	fmt.Printf("📤 Sent auth: %s\n", authMsg[:100])
	
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	_, message, err := conn.ReadMessage()
	if err != nil {
		log.Fatalf("❌ Failed to read auth response: %v", err)
	}
	fmt.Printf("📥 Auth response: %s\n\n", string(message))
	
	pingMsg := fmt.Sprintf(`{"cmd":"ping","request_id":"ping_001","data":{},"timestamp":%d}`, time.Now().UnixMilli())
	err = conn.WriteMessage(websocket.TextMessage, []byte(pingMsg))
	if err != nil {
		log.Fatalf("❌ Failed to send ping: %v", err)
	}
	fmt.Printf("📤 Sent ping: %s\n", pingMsg)
	
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	_, message, err = conn.ReadMessage()
	if err != nil {
		log.Fatalf("❌ Failed to read pong: %v", err)
	}
	fmt.Printf("📥 Pong response: %s\n\n", string(message))
	
	getRoomsMsg := fmt.Sprintf(`{"cmd":"get_room_type_list","request_id":"rooms_001","data":{},"timestamp":%d}`, time.Now().UnixMilli())
	err = conn.WriteMessage(websocket.TextMessage, []byte(getRoomsMsg))
	if err != nil {
		log.Fatalf("❌ Failed to send get_rooms: %v", err)
	}
	fmt.Printf("📤 Sent get_rooms: %s\n", getRoomsMsg)
	
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	_, message, err = conn.ReadMessage()
	if err != nil {
		log.Fatalf("❌ Failed to read rooms response: %v", err)
	}
	fmt.Printf("📥 Rooms response: %s\n", string(message))
	
	fmt.Println("\n✅ WebSocket test completed successfully!")
}
