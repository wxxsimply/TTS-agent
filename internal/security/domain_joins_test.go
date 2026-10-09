package security

import "testing"

func TestValidateDomainJoins(t *testing.T) {
	tests := []struct {
		name    string
		sql     string
		wantErr bool
	}{
		{"valid order product bridge", `SELECT p.category FROM orders o JOIN order_items oi ON oi.order_id=o.id JOIN products p ON p.id=oi.product_id`, false},
		{"invalid coincidental ids", `SELECT p.category FROM orders o JOIN products p ON o.id=p.id`, true},
		{"invalid traffic to orders", `SELECT * FROM traffic_sessions ts JOIN orders o ON ts.user_id=o.user_id`, true},
		{"valid advertising", `SELECT SUM(s.amount) FROM ad_spend s JOIN ad_campaigns c ON c.id=s.campaign_id`, false},
		{"single table", `SELECT COUNT(*) FROM orders`, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateDomainJoins(test.sql, "ecommerce")
			if (err != nil) != test.wantErr {
				t.Fatalf("ValidateDomainJoins() error = %v, wantErr %v", err, test.wantErr)
			}
		})
	}
}
