package security

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	domainRelationPattern = regexp.MustCompile(`(?i)\b(?:FROM|JOIN)\s+"?([a-zA-Z_][\w$]*)"?(?:\s+(?:AS\s+)?([a-zA-Z_][\w$]*))?`)
	qualifiedEquality     = regexp.MustCompile(`(?i)"?([a-zA-Z_][\w$]*)"?\."?([a-zA-Z_][\w$]*)"?\s*=\s*"?([a-zA-Z_][\w$]*)"?\."?([a-zA-Z_][\w$]*)"?`)
)

var sqlKeywords = map[string]bool{
	"where": true, "join": true, "left": true, "right": true, "inner": true,
	"full": true, "cross": true, "on": true, "group": true, "order": true,
	"limit": true, "having": true, "union": true,
}

var ecommerceTables = map[string]bool{
	"users": true, "products": true, "orders": true, "order_items": true,
	"ad_campaigns": true, "ad_spend": true, "traffic_sessions": true,
}

var ecommerceRelationships = map[string]bool{
	joinKey("orders", "user_id", "users", "id"):              true,
	joinKey("order_items", "order_id", "orders", "id"):       true,
	joinKey("order_items", "product_id", "products", "id"):   true,
	joinKey("ad_spend", "campaign_id", "ad_campaigns", "id"): true,
	joinKey("traffic_sessions", "user_id", "users", "id"):    true,
}

// ValidateDomainJoins rejects cross-table equality joins that are not declared
// by the built-in domain. Unknown aliases/CTEs are left to PostgreSQL and the
// normal retry loop so this remains a conservative semantic guard.
func ValidateDomainJoins(sql, domain string) error {
	if domain != "ecommerce" {
		return nil
	}
	aliases := map[string]string{}
	for _, match := range domainRelationPattern.FindAllStringSubmatch(sql, -1) {
		table := strings.ToLower(match[1])
		if !ecommerceTables[table] {
			continue
		}
		alias := table
		if len(match) > 2 && match[2] != "" && !sqlKeywords[strings.ToLower(match[2])] {
			alias = strings.ToLower(match[2])
		}
		aliases[alias] = table
		aliases[table] = table
	}
	for _, match := range qualifiedEquality.FindAllStringSubmatch(sql, -1) {
		leftTable, leftOK := aliases[strings.ToLower(match[1])]
		rightTable, rightOK := aliases[strings.ToLower(match[3])]
		if !leftOK || !rightOK || leftTable == rightTable {
			continue
		}
		if !ecommerceRelationships[joinKey(leftTable, strings.ToLower(match[2]), rightTable, strings.ToLower(match[4]))] {
			return fmt.Errorf("join relationship is not declared: %s.%s = %s.%s", leftTable, match[2], rightTable, match[4])
		}
	}
	return nil
}

func joinKey(leftTable, leftColumn, rightTable, rightColumn string) string {
	left := strings.ToLower(leftTable + "." + leftColumn)
	right := strings.ToLower(rightTable + "." + rightColumn)
	if left > right {
		left, right = right, left
	}
	return left + "=" + right
}
