package schema

import (
	"strings"
	"testing"
)

func TestTableDDLReturnsOnlySelectedTable(t *testing.T) {
	ddl := `
CREATE TABLE users (id SERIAL PRIMARY KEY);
CREATE TABLE orders (id SERIAL PRIMARY KEY, user_id INTEGER REFERENCES users(id));
`
	result := tableDDL(ddl, "orders")
	if !strings.Contains(result, "CREATE TABLE orders") || strings.Contains(result, "CREATE TABLE users") {
		t.Fatalf("unexpected DDL fragment: %s", result)
	}
}
