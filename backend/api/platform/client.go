package platform

import "context"

type Client interface {
	GetBalance(ctx context.Context, req *BalanceRequest) (*BalanceResponse, error)
	Debit(ctx context.Context, req *DebitRequest) (*CommonResponse, error)
	Credit(ctx context.Context, req *CreditRequest) (*CommonResponse, error)
	Settle(ctx context.Context, req *SettleRequest) (*CommonResponse, error)
}
