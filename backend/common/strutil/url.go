// Package strutil 提供项目统一的字符串拼接工具函数，封装标准库能力，
// 消除 fmt.Sprintf 拼接 URL / host:port / 文件路径等带来的安全与一致性问题。
// 规约参考 CODING_STANDARD.md §16 字符串拼接规约（SC-1 ~ SC-10）。
package strutil

import (
	"errors"
	"net/url"
	"strings"
)

// JoinURLPath 拼接 baseURL 与 path，自动处理末尾/前导斜杠，避免双斜杠。
// baseURL 必须是合法的 URL（含 scheme），如 "https://api.example.com"。
// path 为相对路径，可带或不带前导 "/"。
// 例：
//
//	JoinURLPath("https://api.example.com", "balance") -> "https://api.example.com/balance"
//	JoinURLPath("https://api.example.com/", "/balance") -> "https://api.example.com/balance"
func JoinURLPath(baseURL, path string) (string, error) {
	if baseURL == "" {
		return "", errors.New("strutil: baseURL must not be empty")
	}

	base, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}

	// url.JoinPath 会自动处理多余的斜杠
	joined := base.JoinPath(path)
	return joined.String(), nil
}

// BuildURLWithQuery 拼接 baseURL + path + query 参数。
// params 为 nil 或空时，仅返回 path 拼接结果。
// query 参数通过 url.Values.Encode() 进行 URL 编码，避免特殊字符破坏 URL。
// 例：
//
//	BuildURLWithQuery("https://api.example.com", "balance", url.Values{"mid": {"1"}})
//	-> "https://api.example.com/balance?mid=1"
func BuildURLWithQuery(baseURL, path string, params url.Values) (string, error) {
	urlStr, err := JoinURLPath(baseURL, path)
	if err != nil {
		return "", err
	}

	if len(params) == 0 {
		return urlStr, nil
	}

	encoded := params.Encode()
	if encoded == "" {
		return urlStr, nil
	}

	// 已有 query 时追加而非覆盖
	if strings.Contains(urlStr, "?") {
		return urlStr + "&" + encoded, nil
	}
	return urlStr + "?" + encoded, nil
}
