package invoice

import (
	"encoding/json"
	"testing"
)

const payload = `{"number":"INV-1","customer":"Ada","amount_cents":1250,"paid":true}`

func TestParse(t *testing.T) {
	got, err := Parse([]byte(payload))
	if err != nil {
		t.Fatalf("Parse returned %v", err)
	}
	want := Invoice{Number: "INV-1", Customer: "Ada", AmountCents: 1250, Paid: true}
	if got != want {
		t.Errorf("Parse = %+v, want %+v", got, want)
	}
}

func TestRoundTrip(t *testing.T) {
	inv, err := Parse([]byte(payload))
	if err != nil {
		t.Fatalf("Parse returned %v", err)
	}
	b, err := json.Marshal(inv)
	if err != nil {
		t.Fatalf("Marshal returned %v", err)
	}
	var back map[string]any
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("the encoded invoice does not parse: %v", err)
	}
	want := map[string]any{"number": "INV-1", "customer": "Ada", "amount_cents": 1250.0, "paid": true}
	if len(back) != len(want) {
		t.Fatalf("encoded invoice has keys %v, want %v", keys(back), keys(want))
	}
	for k, v := range want {
		if back[k] != v {
			t.Errorf("encoded invoice[%q] = %v, want %v", k, back[k], v)
		}
	}
}

func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
