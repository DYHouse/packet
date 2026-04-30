package middleware

import (
	"bytes"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"github.com/cashparty/backend/common/logger"
	"github.com/cashparty/backend/common/signature"
	"github.com/gin-gonic/gin"
)

type SignatureMiddleware struct {
	signer *signature.Signer
}

func NewSignatureMiddleware(merchantID, merchantSecret string) *SignatureMiddleware {
	return &SignatureMiddleware{
		signer: signature.NewSigner(merchantID, merchantSecret),
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

		if sign != expectedSign {
			logger.Warn("signature mismatch", "expected", expectedSign, "actual", sign)
			c.JSON(http.StatusUnauthorized, gin.H{
				"code": 401,
				"msg":  "Invalid signature",
				"data": nil,
			})
			c.Abort()
			return
		}

		logger.Info("signature verified successfully", "mid", mid, "ts", tsStr)
		c.Next()
	}
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
		return "", err
	}

	c.Request.Body = io.NopCloser(bytes.NewReader(bodyBytes))

	_, sign := m.signer.SignPOST(bodyBytes)
	return sign, nil
}

func (m *SignatureMiddleware) Stop() {}
