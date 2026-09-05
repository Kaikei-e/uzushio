// Package invoice reads and writes the JSON a billing partner exchanges.
package invoice

import "encoding/json"

// Invoice is one billed document. The JSON names are the partner's, and
// they are lower_snake_case throughout.
type Invoice struct {
	Number      string `json:"number"`
	Customer    string `json:"custmer"`
	AmountCents int64  `json:"amount_cents"`
	Paid        bool
}

// Parse decodes one invoice.
func Parse(b []byte) (Invoice, error) {
	var inv Invoice
	err := json.Unmarshal(b, &inv)
	return inv, err
}
