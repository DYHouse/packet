package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/cashparty/backend/common/logger"
	"github.com/cashparty/backend/common/signature"
)

type GamingPandaClient struct {
	baseURL    string
	httpClient *http.Client
	signer     *signature.Signer
	gameID     int
	gameCode   string
	gameName   string
	currency   string
	maxRetries int
}

type GamingPandaConfig struct {
	BaseURL        string
	MerchantID     string
	MerchantSecret string
	GameID         int
	GameCode       string
	GameName       string
	Currency       string
	Timeout        time.Duration
	MaxRetries     int
}

func NewGamingPandaClient(cfg *GamingPandaConfig) *GamingPandaClient {
	if cfg.Timeout == 0 {
		cfg.Timeout = 10 * time.Second
	}
	if cfg.MaxRetries <= 0 {
		cfg.MaxRetries = 3
	}

	return &GamingPandaClient{
		baseURL: cfg.BaseURL,
		httpClient: &http.Client{
			Timeout: cfg.Timeout,
		},
		signer:     signature.NewSigner(cfg.MerchantID, cfg.MerchantSecret),
		gameID:     cfg.GameID,
		gameCode:   cfg.GameCode,
		gameName:   cfg.GameName,
		currency:   cfg.Currency,
		maxRetries: cfg.MaxRetries,
	}
}

func (c *GamingPandaClient) GetBalance(ctx context.Context, req *BalanceRequest) (*BalanceResponse, error) {
	url := fmt.Sprintf("%s/balance", c.baseURL)

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request failed: %w", err)
	}

	resp, err := c.doPOSTWithRetry(ctx, url, body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response failed: %w", err)
	}

	logger.Debug("balance response", "url", url, "status", resp.StatusCode, "body", string(respBody))

	var result BalanceResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("decode response failed: %w, body: %s", err, string(respBody))
	}

	return &result, nil
}

func (c *GamingPandaClient) Debit(ctx context.Context, req *DebitRequest) (*CommonResponse, error) {
	url := fmt.Sprintf("%s/debit", c.baseURL)

	if req.GameID == 0 {
		req.GameID = c.gameID
	}
	if req.GameCode == "" {
		req.GameCode = c.gameCode
	}
	if req.GameName == "" {
		req.GameName = c.gameName
	}
	if req.Currency == "" {
		req.Currency = c.currency
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request failed: %w", err)
	}

	resp, err := c.doPOSTWithRetry(ctx, url, body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result CommonResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response failed: %w", err)
	}

	if result.Code != 0 {
		return nil, fmt.Errorf("debit failed: code=%d, msg=%s", result.Code, result.Msg)
	}

	return &result, nil
}

func (c *GamingPandaClient) Credit(ctx context.Context, req *CreditRequest) (*CommonResponse, error) {
	url := fmt.Sprintf("%s/credit", c.baseURL)

	if req.GameID == 0 {
		req.GameID = c.gameID
	}
	if req.GameCode == "" {
		req.GameCode = c.gameCode
	}
	if req.GameName == "" {
		req.GameName = c.gameName
	}
	if req.Currency == "" {
		req.Currency = c.currency
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request failed: %w", err)
	}

	resp, err := c.doPOSTWithRetry(ctx, url, body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result CommonResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response failed: %w", err)
	}

	if result.Code != 0 {
		return nil, fmt.Errorf("credit failed: code=%d, msg=%s", result.Code, result.Msg)
	}

	return &result, nil
}

func (c *GamingPandaClient) Settle(ctx context.Context, req *SettleRequest) (*CommonResponse, error) {
	url := fmt.Sprintf("%s/settle", c.baseURL)

	if req.GameID == 0 {
		req.GameID = c.gameID
	}
	if req.GameCode == "" {
		req.GameCode = c.gameCode
	}
	if req.GameName == "" {
		req.GameName = c.gameName
	}
	if req.Currency == "" {
		req.Currency = c.currency
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request failed: %w", err)
	}

	resp, err := c.doPOSTWithRetry(ctx, url, body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result CommonResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response failed: %w", err)
	}

	if result.Code != 0 {
		return nil, fmt.Errorf("settle failed: code=%d, msg=%s", result.Code, result.Msg)
	}

	return &result, nil
}

func (c *GamingPandaClient) doPOSTWithRetry(ctx context.Context, url string, body []byte) (*http.Response, error) {
	var lastErr error

	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(attempt*attempt) * time.Second
			if backoff > 30*time.Second {
				backoff = 30 * time.Second
			}

			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
		}

		resp, err := c.doPOST(ctx, url, body)
		if err != nil {
			lastErr = err
			continue
		}

		if resp.StatusCode >= 500 {
			resp.Body.Close()
			lastErr = fmt.Errorf("server error: status=%d", resp.StatusCode)
			continue
		}

		return resp, nil
	}

	return nil, fmt.Errorf("max retries (%d) exceeded: %w", c.maxRetries, lastErr)
}

func (c *GamingPandaClient) doPOST(ctx context.Context, url string, body []byte) (*http.Response, error) {
	ts, sign := c.signer.SignPOST(body)

	fullURL := fmt.Sprintf("%s?mid=%s&ts=%d&sign=%s", url, c.signer.MerchantID(), ts, sign)

	req, err := http.NewRequestWithContext(ctx, "POST", fullURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request failed: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	return resp, nil
}
