package signature

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"
)

type Signer struct {
	merchantID     string
	merchantSecret string
}

func NewSigner(merchantID, merchantSecret string) *Signer {
	return &Signer{
		merchantID:     merchantID,
		merchantSecret: merchantSecret,
	}
}

func (s *Signer) MerchantID() string {
	return s.merchantID
}

func (s *Signer) SignGET(params map[string]string) (ts int64, sign string) {
	ts = currentTimeSeconds()

	sortedKeys := make([]string, 0, len(params))
	for key := range params {
		sortedKeys = append(sortedKeys, key)
	}
	sort.Strings(sortedKeys)

	var jsonPairs []string
	for _, key := range sortedKeys {
		jsonPairs = append(jsonPairs, fmt.Sprintf(`"%s":"%s"`, key, params[key]))
	}

	jsonStr := ""
	if len(jsonPairs) > 0 {
		jsonStr = "{" + strings.Join(jsonPairs, ",") + "}"
	}

	signStr := fmt.Sprintf(`%s{"mid":"%s","ts":"%d"}`, jsonStr, s.merchantID, ts)
	sign = s.computeHMACSHA256(signStr)

	return ts, sign
}

func (s *Signer) SignPOST(body []byte) (ts int64, sign string) {
	ts = currentTimeSeconds()

	signStr := fmt.Sprintf(`%s{"mid":"%s","ts":"%d"}`, string(body), s.merchantID, ts)
	sign = s.computeHMACSHA256(signStr)

	return ts, sign
}

func (s *Signer) VerifyGET(params map[string]string, ts int64, sign string) bool {
	_, expectedSign := s.signGETWithTS(params, ts)
	return sign == expectedSign
}

func (s *Signer) VerifyPOST(body []byte, ts int64, sign string) bool {
	expectedSign := s.signPOSTWithTS(body, ts)
	return sign == expectedSign
}

func (s *Signer) signGETWithTS(params map[string]string, ts int64) (int64, string) {
	sortedKeys := make([]string, 0, len(params))
	for key := range params {
		sortedKeys = append(sortedKeys, key)
	}
	sort.Strings(sortedKeys)

	var jsonPairs []string
	for _, key := range sortedKeys {
		jsonPairs = append(jsonPairs, fmt.Sprintf(`"%s":"%s"`, key, params[key]))
	}

	jsonStr := ""
	if len(jsonPairs) > 0 {
		jsonStr = "{" + strings.Join(jsonPairs, ",") + "}"
	}

	signStr := fmt.Sprintf(`%s{"mid":"%s","ts":"%d"}`, jsonStr, s.merchantID, ts)
	sign := s.computeHMACSHA256(signStr)

	return ts, sign
}

func (s *Signer) signPOSTWithTS(body []byte, ts int64) string {
	signStr := fmt.Sprintf(`%s{"mid":"%s","ts":"%d"}`, string(body), s.merchantID, ts)
	return s.computeHMACSHA256(signStr)
}

func (s *Signer) computeHMACSHA256(data string) string {
	h := hmac.New(sha256.New, []byte(s.merchantSecret))
	h.Write([]byte(data))
	return hex.EncodeToString(h.Sum(nil))
}

func currentTimeSeconds() int64 {
	return time.Now().Unix()
}
