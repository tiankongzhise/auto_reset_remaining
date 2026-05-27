package api

import "testing"

func TestParseBalanceDefaultPaths(t *testing.T) {
	tests := []struct {
		name string
		body string
		want float64
	}{
		{name: "top level", body: `{"balance":0.42}`, want: 0.42},
		{name: "nested user", body: `{"data":{"user":{"balance":"-0.002658"}}}`, want: -0.002658},
		{name: "quota remaining", body: `{"quota":{"remaining":12.5}}`, want: 12.5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseBalance([]byte(tt.body), "")
			if err != nil {
				t.Fatalf("ParseBalance() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("ParseBalance() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseBalancePrefersUserBalanceOverTopLevelZero(t *testing.T) {
	got, err := ParseBalance([]byte(`{"balance":0,"data":{"user":{"balance":"76.25"}}}`), "")
	if err != nil {
		t.Fatalf("ParseBalance() error = %v", err)
	}
	if got != 76.25 {
		t.Fatalf("ParseBalance() = %v, want 76.25", got)
	}
}

func TestParseBalanceRejectsAmbiguousFallback(t *testing.T) {
	_, err := ParseBalance([]byte(`{"payload":{"balance":0,"account":{"remaining":76.25}}}`), "")
	if err == nil {
		t.Fatal("ParseBalance() expected ambiguous fallback error")
	}
}

func TestParseBalanceConfiguredPath(t *testing.T) {
	got, err := ParseBalance([]byte(`{"payload":{"usage":[{"left":"1.25"}]}}`), "payload.usage.0.left")
	if err != nil {
		t.Fatalf("ParseBalance() error = %v", err)
	}
	if got != 1.25 {
		t.Fatalf("ParseBalance() = %v, want 1.25", got)
	}
}

func TestParseBalanceMissingField(t *testing.T) {
	if _, err := ParseBalance([]byte(`{"usage":123}`), "data.balance"); err == nil {
		t.Fatal("ParseBalance() expected error")
	}
}
