package platform

import (
	"fmt"

	"github.com/cashparty/backend/common/config"
)

func NewClient(cfg *config.PlatformConfig) (Client, error) {
	if cfg == nil {
		return NewMockClient(), nil
	}

	switch cfg.Provider {
	case "gamingpanda":
		return NewGamingPandaClient(&GamingPandaConfig{
			BaseURL:        cfg.BaseURL,
			MerchantID:     cfg.MerchantID,
			MerchantSecret: cfg.MerchantSecret,
			GameID:         cfg.GameID,
			GameCode:       cfg.GameCode,
			GameName:       cfg.GameName,
			Currency:       cfg.Currency,
			Timeout:        cfg.Timeout,
			MaxRetries:     cfg.MaxRetries,
		}), nil
	case "mock", "":
		return NewMockClient(), nil
	default:
		return nil, fmt.Errorf("unknown platform provider: %s", cfg.Provider)
	}
}

func NewClientWithDefaults(cfg *config.PlatformConfig) Client {
	client, err := NewClient(cfg)
	if err != nil {
		return NewMockClient()
	}
	return client
}
