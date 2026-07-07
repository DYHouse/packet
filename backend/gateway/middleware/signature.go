package middleware

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/cashparty/backend/common/logger"
	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/cashparty/backend/common/rediskeys"
	"github.com/cashparty/backend/common/signature"
	"github.com/gin-gonic/gin"
)

// maxTimestampSkew 签名时间戳最大偏移（±5 分钟）
const maxTimestampSkew = 5 * time.Minute

// nonceTTL nonce 防重放 key 的 TTL（10 分钟，略大于 maxTimestampSkew 的 2 倍）
const nonceTTL = 10 * time.Minute

type SignatureMiddleware struct {
	signer *signature.Signer
	redis  *cRedis.Client
}

// NewSignatureMiddleware 创建签名校验中间件。
// redis 参数用于 nonce 防重放，若为 nil 则跳过 nonce 检查（仅校验签名与时间戳窗口）。
func NewSignatureMiddleware(merchantID, merchantSecret string, redis *cRedis.Client) *SignatureMiddleware {
	return &SignatureMiddleware{
		signer: signature.NewSigner(merchantID, merchantSecret),
		redis:  redis,
	}
}

func (m *SignatureMiddleware) VerifySignature() gin.HandlerFunc {
	return func(c *gin.Context) {
		mid := c.Query("mid")
		tsStr := c.Query("ts")
		sign := c.Query("sign")

		if mid == "" || tsStr == "" || sign == "" {
			logger.Warn("missing signature parameters", "mid", mid, "ts", tsStr, "sign", sign)
			c.JSON(http.StatusUnauthorized, gin.H{
				"code": 401,
				"msg":  "Missing signature parameters",
				"data": nil,
			})
			c.Abort()
			return
		}

		if mid != m.signer.MerchantID() {
			logger.Warn("invalid merchant id", "expected", m.signer.MerchantID(), "actual", mid)
			c.JSON(http.StatusUnauthorized, gin.H{
				"code": 401,
				"msg":  "Invalid merchant ID",
				"data": nil,
			})
			c.Abort()
			return
		}

		ts, err := strconv.ParseInt(tsStr, 10, 64)
		if err != nil {
			logger.Warn("invalid timestamp", "ts", tsStr, "error", err)
			c.JSON(http.StatusUnauthorized, gin.H{
				"code": 401,
				"msg":  "Invalid timestamp",
				"data": nil,
			})
			c.Abort()
			return
		}

		// 时间戳窗口校验：±5 分钟
		now := time.Now().Unix()
		diff := now - ts
		if diff < 0 {
			diff = -diff
		}
		if diff > int64(maxTimestampSkew.Seconds()) {
			logger.Warn("timestamp out of allowed window", "ts", ts, "now", now, "diff_seconds", diff)
			c.JSON(http.StatusUnauthorized, gin.H{
				"code": 401,
				"msg":  "Timestamp expired",
				"data": nil,
			})
			c.Abort()
			return
		}

		var expectedSign string

		if c.Request.Method == "GET" {
			expectedSign = m.verifyGETSignature(c, ts)
		} else if c.Request.Method == "POST" {
			expectedSign, err = m.verifyPOSTSignature(c, ts)
			if err != nil {
				logger.Error("failed to verify POST signature", "error", err)
				c.JSON(http.StatusInternalServerError, gin.H{
					"code": 500,
					"msg":  "Failed to verify signature",
					"data": nil,
				})
				c.Abort()
				return
			}
		} else {
			c.JSON(http.StatusMethodNotAllowed, gin.H{
				"code": 405,
				"msg":  "Method not allowed",
				"data": nil,
			})
			c.Abort()
			return
		}

		// 使用 hmac.Equal 防止时序攻击（规约 §14 安全规范）
		if !hmac.Equal([]byte(sign), []byte(expectedSign)) {
			logger.Warn("signature mismatch", "expected", expectedSign, "actual", sign)
			c.JSON(http.StatusUnauthorized, gin.H{
				"code": 401,
				"msg":  "Invalid signature",
				"data": nil,
			})
			c.Abort()
			return
		}

		// nonce 防重放：基于请求特征派生 nonce，Redis SetNX 去重
		if m.redis != nil {
			nonce := deriveNonce(c.Request.Method, c.Request.URL.Path, mid, tsStr, sign)
			nonceKey := rediskeys.GatewayNonceKey(nonce)
			ok, err := m.redis.SetNX(c.Request.Context(), nonceKey, "1", nonceTTL).Result()
			if err != nil {
				// Redis 故障时 fail-closed，拒绝请求以防重放
				logger.Error("failed to check nonce in redis", "nonce", nonce, "error", err)
				c.JSON(http.StatusInternalServerError, gin.H{
					"code": 500,
					"msg":  "Failed to verify request",
					"data": nil,
				})
				c.Abort()
				return
			}
			if !ok {
				logger.Warn("replay detected, nonce already exists", "nonce", nonce)
				c.JSON(http.StatusUnauthorized, gin.H{
					"code": 401,
					"msg":  "Request already processed",
					"data": nil,
				})
				c.Abort()
				return
			}
		}

		logger.Info("signature verified successfully", "mid", mid, "ts", tsStr)
		c.Next()
	}
}

// deriveNonce 从请求特征派生 nonce（method + path + mid + ts + sign 的 SHA256 摘要）
func deriveNonce(method, path, mid, ts, sign string) string {
	h := hmac.New(sha256.New, []byte(mid))
	h.Write([]byte(method))
	h.Write([]byte(path))
	h.Write([]byte(ts))
	h.Write([]byte(sign))
	return hex.EncodeToString(h.Sum(nil))
}

func (m *SignatureMiddleware) verifyGETSignature(c *gin.Context, ts int64) string {
	params := url.Values{}
	for key, values := range c.Request.URL.Query() {
		if key != "mid" && key != "ts" && key != "sign" && len(values) > 0 {
			params.Set(key, values[0])
		}
	}

	paramMap := make(map[string]string)
	for key, values := range params {
		if len(values) > 0 {
			paramMap[key] = values[0]
		}
	}

	_, sign := m.signer.SignGET(paramMap)
	return sign
}

func (m *SignatureMiddleware) verifyPOSTSignature(c *gin.Context, ts int64) (string, error) {
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return "", fmt.Errorf("read request body failed: %w", err)
	}

	c.Request.Body = io.NopCloser(bytes.NewReader(bodyBytes))

	_, sign := m.signer.SignPOST(bodyBytes)
	return sign, nil
}

func (m *SignatureMiddleware) Stop() {}
