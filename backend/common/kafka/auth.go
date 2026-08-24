package kafka

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"

	"github.com/aws/aws-msk-iam-sasl-signer-go/signer"
	"github.com/cashparty/backend/common/logger"
	"github.com/segmentio/kafka-go"
	"github.com/segmentio/kafka-go/sasl"
	"github.com/segmentio/kafka-go/sasl/scram"
)

// OAuthTokenProvider 提供 OAUTHBEARER 认证所需的 Token。
type OAuthTokenProvider interface {
	Token() (string, error)
}

// MSKTokenProvider 使用 AWS MSK IAM SASL Signer 生成基于 IAM 的 OAUTHBEARER Token。
// 每次调用 Token() 都会重新生成，不缓存过期 Token。
type MSKTokenProvider struct {
	Region string
}

func (m *MSKTokenProvider) Token() (string, error) {
	token, _, err := signer.GenerateAuthToken(context.TODO(), m.Region)
	if err != nil {
		return "", fmt.Errorf("generate MSK IAM token failed: %w", err)
	}
	return token, nil
}

// oauthBearerMechanism 实现 SASL/OAUTHBEARER 机制（RFC 7628）。
// kafka-go v0.4.47 不内置 OAUTHBEARER 支持，此处自行实现。
type oauthBearerMechanism struct {
	tokenProvider OAuthTokenProvider
}

func (m *oauthBearerMechanism) Name() string {
	return "OAUTHBEARER"
}

func (m *oauthBearerMechanism) Start(ctx context.Context) (sasl.StateMachine, []byte, error) {
	token, err := m.tokenProvider.Token()
	if err != nil {
		return nil, nil, err
	}
	// OAUTHBEARER 初始响应格式：GS2 header(n,,) + 0x01 + kv-pairs + 0x01 0x01
	// 参考 RFC 7628 Section 3.1
	initialResp := []byte("n,,\x01auth=Bearer " + token + "\x01\x01")
	return &oauthBearerSession{}, initialResp, nil
}

// oauthBearerSession 处理 OAUTHBEARER 的 challenge/response。
type oauthBearerSession struct{}

func (s *oauthBearerSession) Next(ctx context.Context, challenge []byte) (bool, []byte, error) {
	// 空 challenge 表示认证成功
	if len(challenge) == 0 {
		return true, nil, nil
	}
	// 非空 challenge 包含服务端错误信息（JSON 格式）
	return false, nil, fmt.Errorf("OAUTHBEARER authentication failed: %s", string(challenge))
}

// buildSASLMechanism 按 SASL 配置构造 sasl.Mechanism。明文模式返回 nil。
func buildSASLMechanism(cfg SASLConfig) sasl.Mechanism {
	switch cfg.Mechanism {
	case "", "none":
		return nil
	case "OAUTHBEARER":
		logger.Info("kafka SASL using OAUTHBEARER (MSK IAM)", "region", cfg.Region)
		return &oauthBearerMechanism{
			tokenProvider: &MSKTokenProvider{Region: cfg.Region},
		}
	case "SCRAM-SHA-512":
		logger.Info("kafka SASL using SCRAM-SHA-512")
		m, err := scram.Mechanism(scram.SHA512, cfg.Username, cfg.Password)
		if err != nil {
			logger.Error("failed to create SCRAM-SHA-512 mechanism", "error", err)
			return nil
		}
		return m
	case "SCRAM-SHA-256":
		logger.Info("kafka SASL using SCRAM-SHA-256")
		m, err := scram.Mechanism(scram.SHA256, cfg.Username, cfg.Password)
		if err != nil {
			logger.Error("failed to create SCRAM-SHA-256 mechanism", "error", err)
			return nil
		}
		return m
	default:
		logger.Warn("unknown kafka SASL mechanism, falling back to plaintext", "mechanism", cfg.Mechanism)
		return nil
	}
}

// buildTLSConfig 构造 *tls.Config。未启用时返回 nil。
func buildTLSConfig(cfg TLSConfig) *tls.Config {
	if !cfg.Enabled {
		return nil
	}
	tlsCfg := &tls.Config{
		InsecureSkipVerify: cfg.InsecureSkipVerify,
	}
	if cfg.CAPath != "" {
		caCert, err := os.ReadFile(cfg.CAPath)
		if err != nil {
			logger.Error("failed to read kafka CA cert", "path", cfg.CAPath, "error", err)
			return tlsCfg
		}
		pool := x509.NewCertPool()
		pool.AppendCertsFromPEM(caCert)
		tlsCfg.RootCAs = pool
	}
	return tlsCfg
}

// buildTransport 为 kafka.Writer 构造 Transport。明文模式返回空 Transport（非 nil）。
func buildTransport(saslCfg SASLConfig, tlsCfg TLSConfig) *kafka.Transport {
	mechanism := buildSASLMechanism(saslCfg)
	tls := buildTLSConfig(tlsCfg)
	// 明文模式（无 SASL 无 TLS）返回空 Transport，避免 Writer.Transport 为 nil 时 panic
	if mechanism == nil && tls == nil {
		return &kafka.Transport{}
	}
	return &kafka.Transport{
		SASL: mechanism,
		TLS:  tls,
	}
}

// buildDialer 为 kafka.Reader 构造 Dialer。明文模式返回 nil。
func buildDialer(saslCfg SASLConfig, tlsCfg TLSConfig) *kafka.Dialer {
	mechanism := buildSASLMechanism(saslCfg)
	tls := buildTLSConfig(tlsCfg)
	// 明文模式（无 SASL 无 TLS）返回 nil，保持与改造前完全一致
	if mechanism == nil && tls == nil {
		return nil
	}
	return &kafka.Dialer{
		SASLMechanism: mechanism,
		TLS:           tls,
	}
}
