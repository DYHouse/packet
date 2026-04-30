package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	baseURL        = "http://43.135.35.31"
	merchantID     = "10001"
	merchantSecret = "aca5d11a-e481-4163-9505-194564558ae3"
)

func main() {
	fmt.Println("=== Testing Game APIs ===\n")

	fmt.Println("1. Testing Health Check...")
	testHealthCheck()

	fmt.Println("\n2. Testing Redpacket...")
	testRedpacket()

	fmt.Println("\n3. Testing Game List API...")
	testGameList()

	fmt.Println("\n4. Testing Game Start API...")
	testGameStart()
}

func testHealthCheck() {
	resp, err := http.Get(baseURL + "/health")
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	fmt.Printf("Response: %s\n", string(body))
}

func testRedpacket() {
	resp, err := http.Get(baseURL + "/redpacket")
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	fmt.Printf("Response: %s\n", string(body))
}

func testGameList() {
	ts := time.Now().Unix()
	params := url.Values{}

	sign := generateGETSignature(params, ts)

	fullURL := fmt.Sprintf("%s/game/list?mid=%s&ts=%d&sign=%s",
		baseURL, merchantID, ts, sign)

	fmt.Printf("Request URL: %s\n", fullURL)

	resp, err := http.Get(fullURL)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	fmt.Printf("Response Status: %d\n", resp.StatusCode)
	fmt.Printf("Response Body: %s\n", string(body))
}

func testGameStart() {
	ts := time.Now().Unix()

	// 按照字母顺序排列的JSON
	bodyStr := `{"client_ip":"127.0.0.1","currency":"USD","game_code":"redpacket","lang":"en","user_id":"test_user_123","username":"TestUser","version":"1.1"}`

	sign := generatePOSTSignature(bodyStr, ts)

	fullURL := fmt.Sprintf("%s/game/start?mid=%s&ts=%d&sign=%s",
		baseURL, merchantID, ts, sign)

	fmt.Printf("Request URL: %s\n", fullURL)
	fmt.Printf("Request Body: %s\n", bodyStr)

	resp, err := http.Post(fullURL, "application/json", strings.NewReader(bodyStr))
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	fmt.Printf("Response Status: %d\n", resp.StatusCode)
	fmt.Printf("Response Body: %s\n", string(body))
}

func generateGETSignature(params url.Values, ts int64) string {
	paramMap := make(map[string]string)
	for key, values := range params {
		if len(values) > 0 {
			paramMap[key] = values[0]
		}
	}

	sortedKeys := make([]string, 0, len(paramMap))
	for key := range paramMap {
		sortedKeys = append(sortedKeys, key)
	}
	sort.Strings(sortedKeys)

	var jsonPairs []string
	for _, key := range sortedKeys {
		jsonPairs = append(jsonPairs, fmt.Sprintf(`"%s":"%s"`, key, paramMap[key]))
	}

	jsonStr := ""
	if len(jsonPairs) > 0 {
		jsonStr = "{" + strings.Join(jsonPairs, ",") + "}"
	}

	signStr := fmt.Sprintf(`%s{"mid":"%s","ts":"%s"}`, jsonStr, merchantID, strconv.FormatInt(ts, 10))

	h := hmac.New(sha256.New, []byte(merchantSecret))
	h.Write([]byte(signStr))
	return hex.EncodeToString(h.Sum(nil))
}

func generatePOSTSignature(body string, ts int64) string {
	signStr := fmt.Sprintf(`%s{"mid":"%s","ts":"%s"}`, body, merchantID, strconv.FormatInt(ts, 10))

	h := hmac.New(sha256.New, []byte(merchantSecret))
	h.Write([]byte(signStr))
	return hex.EncodeToString(h.Sum(nil))
}
