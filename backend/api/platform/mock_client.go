package platform

import (
	"context"
	"fmt"
	"sync"
)

type MockClient struct {
	mu           sync.RWMutex
	balances     map[string]int64
	transactions map[string]*CommonResponse
	failMode     bool
	failError    error
	gameCode     string
	gameName     string
	currency     string
}

func NewMockClient() *MockClient {
	m := &MockClient{
		balances:     make(map[string]int64),
		transactions: make(map[string]*CommonResponse),
		gameCode:     "gp_classic_4",
		gameName:     "cashparty",
		currency:     "PHP",
	}

	for i := 1; i <= 1000; i++ {
		userID := fmt.Sprintf("mock_user_%d", 100000+i)
		m.balances[userID] = 1000000
	}

	return m
}

func (m *MockClient) SetBalance(userID string, balance int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.balances[userID] = balance
}

func (m *MockClient) GetUserBalance(userID string) int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.balances[userID]
}

func (m *MockClient) SetFailMode(fail bool, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failMode = fail
	m.failError = err
}

func (m *MockClient) SetGameInfo(gameCode, gameName, currency string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gameCode = gameCode
	m.gameName = gameName
	m.currency = currency
}

func (m *MockClient) GetBalance(ctx context.Context, req *BalanceRequest) (*BalanceResponse, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.failMode {
		return nil, m.failError
	}

	balance, exists := m.balances[req.UserID]
	if !exists {
		balance = 1000000
	}

	return &BalanceResponse{
		Code: 0,
		Msg:  "",
		Data: struct {
			Balance struct {
				UserID   string `json:"user_id"`
				Currency string `json:"currency"`
				Amount   string `json:"amount"`
			} `json:"balance"`
		}{
			Balance: struct {
				UserID   string `json:"user_id"`
				Currency string `json:"currency"`
				Amount   string `json:"amount"`
			}{
				UserID:   req.UserID,
				Currency: req.Currency,
				Amount:   FormatAmount(balance),
			},
		},
	}, nil
}

func (m *MockClient) Debit(ctx context.Context, req *DebitRequest) (*CommonResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.failMode {
		return nil, m.failError
	}

	if cached, exists := m.transactions[req.BizID]; exists {
		return cached, nil
	}

	amount, err := ParseAmount(req.Amount)
	if err != nil {
		return nil, fmt.Errorf("invalid amount: %w", err)
	}

	balance, exists := m.balances[req.UserID]
	if !exists {
		balance = 1000000
	}

	if balance < amount {
		return nil, fmt.Errorf("insufficient balance: current=%s, required=%s", FormatAmount(balance), FormatAmount(amount))
	}

	newBalance := balance - amount

	result := &CommonResponse{
		Code: 0,
		Msg:  "",
		Data: struct {
			Balance struct {
				UserID   string `json:"user_id"`
				Currency string `json:"currency"`
				Amount   string `json:"amount"`
			} `json:"balance"`
		}{
			Balance: struct {
				UserID   string `json:"user_id"`
				Currency string `json:"currency"`
				Amount   string `json:"amount"`
			}{
				UserID:   req.UserID,
				Currency: req.Currency,
				Amount:   FormatAmount(newBalance),
			},
		},
	}

	m.transactions[req.BizID] = result

	return result, nil
}

func (m *MockClient) Credit(ctx context.Context, req *CreditRequest) (*CommonResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.failMode {
		return nil, m.failError
	}

	if cached, exists := m.transactions[req.BizID]; exists {
		return cached, nil
	}

	amount, err := ParseAmount(req.Amount)
	if err != nil {
		return nil, fmt.Errorf("invalid amount: %w", err)
	}

	balance, exists := m.balances[req.UserID]
	if !exists {
		balance = 0
	}

	newBalance := balance + amount

	result := &CommonResponse{
		Code: 0,
		Msg:  "",
		Data: struct {
			Balance struct {
				UserID   string `json:"user_id"`
				Currency string `json:"currency"`
				Amount   string `json:"amount"`
			} `json:"balance"`
		}{
			Balance: struct {
				UserID   string `json:"user_id"`
				Currency string `json:"currency"`
				Amount   string `json:"amount"`
			}{
				UserID:   req.UserID,
				Currency: req.Currency,
				Amount:   FormatAmount(newBalance),
			},
		},
	}

	m.transactions[req.BizID] = result

	return result, nil
}

func (m *MockClient) Settle(ctx context.Context, req *SettleRequest) (*CommonResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.failMode {
		return nil, m.failError
	}

	if cached, exists := m.transactions[req.BizID]; exists {
		return cached, nil
	}

	// Settle(/settle) only records game result, does not move funds.
	// Funds have already been moved via Debit + Credit.
	balance, exists := m.balances[req.UserID]
	if !exists {
		balance = 0
	}

	result := &CommonResponse{
		Code: 0,
		Msg:  "",
		Data: struct {
			Balance struct {
				UserID   string `json:"user_id"`
				Currency string `json:"currency"`
				Amount   string `json:"amount"`
			} `json:"balance"`
		}{
			Balance: struct {
				UserID   string `json:"user_id"`
				Currency string `json:"currency"`
				Amount   string `json:"amount"`
			}{
				UserID:   req.UserID,
				Currency: req.Currency,
				Amount:   FormatAmount(balance),
			},
		},
	}

	m.transactions[req.BizID] = result

	return result, nil
}
