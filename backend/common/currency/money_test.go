package currency

import (
	"encoding/json"
	"testing"
)

func TestMoneyMarshalJSON(t *testing.T) {
	tests := []struct {
		input Money
		want  string
	}{
		{0, "0.00"},
		{1, "0.01"},
		{99, "0.99"},
		{100, "1.00"},
		{1250, "12.50"},
		{1200, "12.00"},
		{100000, "1000.00"},
		{-500, "-5.00"},
		{-1, "-0.01"},
	}

	for _, tt := range tests {
		data, err := json.Marshal(tt.input)
		if err != nil {
			t.Errorf("MarshalJSON(%d) error: %v", tt.input, err)
			continue
		}
		if string(data) != tt.want {
			t.Errorf("MarshalJSON(%d) = %s, want %s", tt.input, string(data), tt.want)
		}
	}
}

func TestMoneyUnmarshalJSON(t *testing.T) {
	tests := []struct {
		input string
		want  Money
	}{
		{"0.00", 0},
		{"0.01", 1},
		{"12.50", 1250},
		{"12.00", 1200},
		{"1000.00", 100000},
		{"-5.00", -500},
		{`"12.50"`, 1250},
		{`"0.00"`, 0},
	}

	for _, tt := range tests {
		var m Money
		err := json.Unmarshal([]byte(tt.input), &m)
		if err != nil {
			t.Errorf("UnmarshalJSON(%s) error: %v", tt.input, err)
			continue
		}
		if m != tt.want {
			t.Errorf("UnmarshalJSON(%s) = %d, want %d", tt.input, m, tt.want)
		}
	}
}

func TestMoneyRoundTrip(t *testing.T) {
	values := []Money{0, 1, 99, 100, 1250, 1200, 100000, -500}
	for _, original := range values {
		data, err := json.Marshal(original)
		if err != nil {
			t.Errorf("Marshal(%d) error: %v", original, err)
			continue
		}
		var decoded Money
		err = json.Unmarshal(data, &decoded)
		if err != nil {
			t.Errorf("Unmarshal(%s) error: %v", string(data), err)
			continue
		}
		if decoded != original {
			t.Errorf("roundtrip: got %d, want %d", decoded, original)
		}
	}
}

func TestMoneyInStruct(t *testing.T) {
	type TestStruct struct {
		Amount Money `json:"amount"`
	}

	// Marshal
	s := TestStruct{Amount: 1250}
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("Marshal struct error: %v", err)
	}
	want := `{"amount":12.50}`
	if string(data) != want {
		t.Errorf("Marshal struct = %s, want %s", string(data), want)
	}

	// Unmarshal from number
	var s2 TestStruct
	err = json.Unmarshal([]byte(`{"amount":12.50}`), &s2)
	if err != nil {
		t.Fatalf("Unmarshal struct from number error: %v", err)
	}
	if s2.Amount != 1250 {
		t.Errorf("Unmarshal from number: got %d, want 1250", s2.Amount)
	}

	// Unmarshal from string
	var s3 TestStruct
	err = json.Unmarshal([]byte(`{"amount":"12.50"}`), &s3)
	if err != nil {
		t.Fatalf("Unmarshal struct from string error: %v", err)
	}
	if s3.Amount != 1250 {
		t.Errorf("Unmarshal from string: got %d, want 1250", s3.Amount)
	}
}

func TestMoneyMethods(t *testing.T) {
	m := Money(1250)
	if m.Fen() != 1250 {
		t.Errorf("Fen() = %d, want 1250", m.Fen())
	}
	if m.Yuan() != 12.50 {
		t.Errorf("Yuan() = %f, want 12.50", m.Yuan())
	}
	if m.String() != "12.50" {
		t.Errorf("String() = %s, want 12.50", m.String())
	}

	m2 := NewMoneyFromFen(500)
	if m2 != 500 {
		t.Errorf("NewMoneyFromFen(500) = %d, want 500", m2)
	}

	m3 := NewMoneyFromYuan(12.50)
	if m3 != 1250 {
		t.Errorf("NewMoneyFromYuan(12.50) = %d, want 1250", m3)
	}
}

func TestParseAmountCompat(t *testing.T) {
	tests := []struct {
		input string
		want  int64
	}{
		{"12.50", 1250},
		{"12.00", 1200},
		{"1.5", 150},
		{"0.01", 1},
		{"0", 0},
		{"", 0},
	}

	for _, tt := range tests {
		got, err := ParseAmount(tt.input)
		if err != nil {
			t.Errorf("ParseAmount(%q) error: %v", tt.input, err)
			continue
		}
		if got != tt.want {
			t.Errorf("ParseAmount(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestFormatAmountCompat(t *testing.T) {
	tests := []struct {
		input int64
		want  string
	}{
		{1250, "12.50"},
		{1200, "12.00"},
		{100, "1.00"},
		{1, "0.01"},
		{0, "0.00"},
	}

	for _, tt := range tests {
		got := FormatAmount(tt.input)
		if got != tt.want {
			t.Errorf("FormatAmount(%d) = %s, want %s", tt.input, got, tt.want)
		}
	}
}
