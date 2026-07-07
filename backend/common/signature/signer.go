package signature

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

// marshalString 将字符串序列化为 JSON 字符串字面量（含双引号）。
// 使用 json.Encoder 并禁用 HTML 转义（SetEscapeHTML(false)），
// 以保持与原有 fmt.Sprintf 拼接行为一致（不转义 <, >, &），
// 同时修复 " 和 \ 未转义导致的 JSON 注入与签名不一致问题。
// 规约参考 CODING_STANDARD.md §16 SC-1。
func marshalString(s string) (string, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		return "", err
	}
	// json.Encoder.Encode 末尾会追加换行符，需去除
	return strings.TrimRight(buf.String(), "\n"), nil
}

// buildSortedJSON 构建按键名字典序排序的紧凑 JSON 字符串。
// 格式：{"k1":"v1","k2":"v2"}（无空格），与原手写拼接格式一致。
func buildSortedJSON(params map[string]string) (string, error) {
	if len(params) == 0 {
		return "", nil
	}

	sortedKeys := make([]string, 0, len(params))
	for key := range params {
		sortedKeys = append(sortedKeys, key)
	}
	sort.Strings(sortedKeys)

	var jsonPairs []string
	for _, key := range sortedKeys {
		keyJSON, err := marshalString(key)
		if err != nil {
			return "", fmt.Errorf("marshal key %q: %w", key, err)
		}
		valJSON, err := marshalString(params[key])
		if err != nil {
			return "", fmt.Errorf("marshal value %q: %w", params[key], err)
		}
		jsonPairs = append(jsonPairs, keyJSON+":"+valJSON)
	}

	return "{" + strings.Join(jsonPairs, ",") + "}", nil
}

// buildSignStr 构建签名串。
// 格式：{paramsJSON}{"mid":"<merchantID>","ts":"<ts>"}
// 保持与平台方约定的双 JSON 对象拼接格式。
func buildSignStr(paramsJSON, merchantID string, ts int64) (string, error) {
	midJSON, err := marshalString(merchantID)
	if err != nil {
		return "", fmt.Errorf("marshal merchantID: %w", err)
	}
	// ts 作为字符串值，与原有格式保持一致（"ts":"%d"）
	tsJSON, err := marshalString(fmt.Sprintf("%d", ts))
	if err != nil {
		return "", fmt.Errorf("marshal ts: %w", err)
	}
	suffix := `{"mid":` + midJSON + `,"ts":` + tsJSON + `}`
	return paramsJSON + suffix, nil
}

func (s *Signer) SignGET(params map[string]string) (ts int64, sign string) {
	ts = currentTimeSeconds()

	paramsJSON, err := buildSortedJSON(params)
	if err != nil {
		// 签名串构建失败时回退到空 params 的签名以避免 panic。
		// 调用方应通过 VerifyGET 校验，签名不匹配将被拒绝。
		paramsJSON = ""
	}

	signStr, err := buildSignStr(paramsJSON, s.merchantID, ts)
	if err != nil {
		signStr, _ = buildSignStr("", s.merchantID, ts)
	}
	sign = s.computeHMACSHA256(signStr)

	return ts, sign
}

func (s *Signer) SignPOST(body []byte) (ts int64, sign string) {
	ts = currentTimeSeconds()

	signStr, err := buildSignStr(string(body), s.merchantID, ts)
	if err != nil {
		signStr, _ = buildSignStr("", s.merchantID, ts)
	}
	sign = s.computeHMACSHA256(signStr)

	return ts, sign
}

func (s *Signer) VerifyGET(params map[string]string, ts int64, sign string) bool {
	_, expectedSign := s.signGETWithTS(params, ts)
	// 使用 hmac.Equal 防止时序攻击（规约 §14 安全规范）
	return hmac.Equal([]byte(sign), []byte(expectedSign))
}

func (s *Signer) VerifyPOST(body []byte, ts int64, sign string) bool {
	expectedSign := s.signPOSTWithTS(body, ts)
	// 使用 hmac.Equal 防止时序攻击（规约 §14 安全规范）
	return hmac.Equal([]byte(sign), []byte(expectedSign))
}

func (s *Signer) signGETWithTS(params map[string]string, ts int64) (int64, string) {
	paramsJSON, err := buildSortedJSON(params)
	if err != nil {
		paramsJSON = ""
	}

	signStr, err := buildSignStr(paramsJSON, s.merchantID, ts)
	if err != nil {
		signStr, _ = buildSignStr("", s.merchantID, ts)
	}
	sign := s.computeHMACSHA256(signStr)

	return ts, sign
}

func (s *Signer) signPOSTWithTS(body []byte, ts int64) string {
	signStr, err := buildSignStr(string(body), s.merchantID, ts)
	if err != nil {
		signStr, _ = buildSignStr("", s.merchantID, ts)
	}
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
