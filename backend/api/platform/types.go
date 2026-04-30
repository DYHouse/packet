package platform

type BalanceRequest struct {
	UserID   string `json:"user_id"`
	Currency string `json:"currency"`
}

type BalanceResponse struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		Balance struct {
			UserID   string `json:"user_id"`
			Currency string `json:"currency"`
			Amount   string `json:"amount"`
		} `json:"balance"`
	} `json:"data"`
}

type DebitRequest struct {
	BizID    string `json:"biz_id"`
	RoundID  string `json:"round_id"`
	GameID   int    `json:"game_id,omitempty"`
	GameCode string `json:"game_code"`
	UserID   string `json:"user_id"`
	Currency string `json:"currency"`
	Amount   string `json:"amount"`
	Reason   string `json:"reason"`
	GameName string `json:"game_name"`
}

type CreditRequest struct {
	BizID    string `json:"biz_id"`
	RoundID  string `json:"round_id"`
	GameID   int    `json:"game_id,omitempty"`
	GameCode string `json:"game_code"`
	UserID   string `json:"user_id"`
	Currency string `json:"currency"`
	Amount   string `json:"amount"`
	Reason   string `json:"reason"`
	GameName string `json:"game_name"`
}

type SettleRequest struct {
	BizID           string `json:"biz_id"`
	RoundID         string `json:"round_id"`
	GameID          int    `json:"game_id,omitempty"`
	GameCode        string `json:"game_code"`
	GameName        string `json:"game_name"`
	UserID          string `json:"user_id"`
	Currency        string `json:"currency"`
	BetAmount       string `json:"bet_amount"`
	PayOut          string `json:"pay_out"`
	Multiplier      string `json:"multiplier"`
	StartTime       int64  `json:"start_time"`
	EndTime         int64  `json:"end_time"`
	Result          string `json:"result"`
	ActualBetAmount string `json:"actual_bet_amount"`
}

type CommonResponse struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		Balance struct {
			UserID   string `json:"user_id"`
			Currency string `json:"currency"`
			Amount   string `json:"amount"`
		} `json:"balance"`
	} `json:"data"`
}
