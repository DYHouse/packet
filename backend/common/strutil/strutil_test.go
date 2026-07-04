package strutil

import (
	"net/url"
	"testing"
)

func TestJoinURLPath(t *testing.T) {
	tests := []struct {
		name     string
		baseURL  string
		path     string
		expected string
		wantErr  bool
	}{
		{
			name:     "baseURL without trailing slash, path without leading slash",
			baseURL:  "https://api.example.com",
			path:     "balance",
			expected: "https://api.example.com/balance",
		},
		{
			name:     "baseURL with trailing slash, path with leading slash",
			baseURL:  "https://api.example.com/",
			path:     "/balance",
			expected: "https://api.example.com/balance",
		},
		{
			name:     "baseURL with trailing slash, path without leading slash",
			baseURL:  "https://api.example.com/",
			path:     "balance",
			expected: "https://api.example.com/balance",
		},
		{
			name:     "multi-segment path",
			baseURL:  "https://api.example.com",
			path:     "/v1/users/balance",
			expected: "https://api.example.com/v1/users/balance",
		},
		{
			name:     "empty path",
			baseURL:  "https://api.example.com",
			path:     "",
			expected: "https://api.example.com",
		},
		{
			name:    "empty baseURL",
			baseURL: "",
			path:    "balance",
			wantErr: true,
		},
		{
			name:     "baseURL with path",
			baseURL:  "https://api.example.com/api",
			path:     "balance",
			expected: "https://api.example.com/api/balance",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := JoinURLPath(tt.baseURL, tt.path)
			if (err != nil) != tt.wantErr {
				t.Errorf("JoinURLPath() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && got != tt.expected {
				t.Errorf("JoinURLPath() = %q, expected %q", got, tt.expected)
			}
		})
	}
}

func TestBuildURLWithQuery(t *testing.T) {
	tests := []struct {
		name     string
		baseURL  string
		path     string
		params   url.Values
		expected string
		wantErr  bool
	}{
		{
			name:     "single param",
			baseURL:  "https://api.example.com",
			path:     "balance",
			params:   url.Values{"mid": {"1"}},
			expected: "https://api.example.com/balance?mid=1",
		},
		{
			name:    "multiple params",
			baseURL: "https://api.example.com",
			path:    "balance",
			params: url.Values{
				"mid": {"1"},
				"ts":  {"1234567890"},
			},
			// url.Values.Encode() sorts keys alphabetically
			expected: "https://api.example.com/balance?mid=1&ts=1234567890",
		},
		{
			name:     "empty params",
			baseURL:  "https://api.example.com",
			path:     "balance",
			params:   url.Values{},
			expected: "https://api.example.com/balance",
		},
		{
			name:     "nil params",
			baseURL:  "https://api.example.com",
			path:     "balance",
			params:   nil,
			expected: "https://api.example.com/balance",
		},
		{
			name:     "special chars in params",
			baseURL:  "https://api.example.com",
			path:     "callback",
			params:   url.Values{"url": {"https://other.com/path?a=1&b=2"}},
			expected: "https://api.example.com/callback?url=https%3A%2F%2Fother.com%2Fpath%3Fa%3D1%26b%3D2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := BuildURLWithQuery(tt.baseURL, tt.path, tt.params)
			if (err != nil) != tt.wantErr {
				t.Errorf("BuildURLWithQuery() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && got != tt.expected {
				t.Errorf("BuildURLWithQuery() = %q, expected %q", got, tt.expected)
			}
		})
	}
}

func TestJoinHostPort(t *testing.T) {
	tests := []struct {
		name     string
		host     string
		port     int
		expected string
	}{
		{
			name:     "IPv4",
			host:     "127.0.0.1",
			port:     8080,
			expected: "127.0.0.1:8080",
		},
		{
			name:     "IPv6 loopback",
			host:     "::1",
			port:     8080,
			expected: "[::1]:8080",
		},
		{
			name:     "IPv6 full",
			host:     "2001:db8::1",
			port:     443,
			expected: "[2001:db8::1]:443",
		},
		{
			name:     "empty host",
			host:     "",
			port:     8080,
			expected: ":8080",
		},
		{
			name:     "hostname",
			host:     "example.com",
			port:     80,
			expected: "example.com:80",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := JoinHostPort(tt.host, tt.port)
			if got != tt.expected {
				t.Errorf("JoinHostPort(%q, %d) = %q, expected %q", tt.host, tt.port, got, tt.expected)
			}
		})
	}
}
