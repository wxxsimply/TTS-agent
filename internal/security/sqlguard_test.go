package security

import "testing"

func TestValidSelect(t *testing.T) {
	r := ValidateSQL("SELECT id, name FROM users LIMIT 10")
	if !r.Valid {
		t.Fatal(r.Error)
	}
	if !limitPattern.MatchString(r.SanitizedSQL) {
		t.Fatal("expected LIMIT")
	}
	if !stringsHasSuffix(r.SanitizedSQL, "AS agent_guarded_result LIMIT 1000") {
		t.Fatalf("expected outer row guard, got %q", r.SanitizedSQL)
	}
}

func TestAutoAddLimit(t *testing.T) {
	r := ValidateSQL("SELECT * FROM orders")
	if !r.Valid {
		t.Fatal(r.Error)
	}
	if !stringsHasSuffix(r.SanitizedSQL, "LIMIT 1000") {
		t.Fatalf("got %q", r.SanitizedSQL)
	}
}

func TestBlockDrop(t *testing.T) {
	if ValidateSQL("DROP TABLE users").Valid {
		t.Fatal("expected block")
	}
}

func TestBlockDelete(t *testing.T) {
	if ValidateSQL("DELETE FROM orders WHERE id = 1").Valid {
		t.Fatal("expected block")
	}
}

func TestBlockMultiStatement(t *testing.T) {
	if ValidateSQL("SELECT 1; DROP TABLE users").Valid {
		t.Fatal("expected block")
	}
}

func TestBlockUpdate(t *testing.T) {
	if ValidateSQL("UPDATE users SET name = 'hack'").Valid {
		t.Fatal("expected block")
	}
}

func TestAllowDangerousWordInsideStringLiteral(t *testing.T) {
	if !ValidateSQL("SELECT id FROM users WHERE name = 'drop table'").Valid {
		t.Fatal("string literal must not be treated as a keyword")
	}
}

func TestBlockCommentsAndSystemAccess(t *testing.T) {
	cases := []string{
		"SELECT * FROM users -- bypass",
		"SELECT * FROM /* hidden */ users",
		"SELECT * FROM pg_catalog.pg_tables",
		"SELECT pg_sleep(10)",
		"SELECT pg_read_file('/etc/passwd')",
		"SELECT * INTO copied_users FROM users",
		"SELECT * FROM users FOR UPDATE",
	}
	for _, sql := range cases {
		if ValidateSQL(sql).Valid {
			t.Errorf("expected SQL to be blocked: %s", sql)
		}
	}
}

func TestOuterLimitCannotBeBypassedByNestedLimit(t *testing.T) {
	result := ValidateSQL("SELECT * FROM (SELECT * FROM orders LIMIT 100000) nested")
	if !result.Valid || !stringsHasSuffix(result.SanitizedSQL, "AS agent_guarded_result LIMIT 1000") {
		t.Fatalf("outer limit missing: %q", result.SanitizedSQL)
	}
}

func TestAllowCTE(t *testing.T) {
	if !ValidateSQL("WITH cte AS (SELECT id FROM users) SELECT * FROM cte LIMIT 10").Valid {
		t.Fatal("expected valid")
	}
}

func TestDatasetTableValidation(t *testing.T) {
	if err := ValidateDatasetTables(`SELECT amount FROM dataset_abc LIMIT 10`, "dataset_abc"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateDatasetTables(`WITH totals AS (SELECT SUM(amount) AS value FROM dataset_abc) SELECT * FROM totals LIMIT 10`, "dataset_abc"); err != nil {
		t.Fatal(err)
	}
}

func TestDatasetTableValidationBlocksOtherTables(t *testing.T) {
	if err := ValidateDatasetTables(`SELECT * FROM app_users JOIN dataset_abc ON true LIMIT 10`, "dataset_abc"); err == nil {
		t.Fatal("expected access to another table to be blocked")
	}
}

func TestDatasetOutputValidation(t *testing.T) {
	valid := `SELECT col_1 AS "订单号", SUM(col_2) AS "销售额" FROM dataset_abc GROUP BY col_1 LIMIT 10`
	if err := ValidateDatasetOutput(valid); err != nil {
		t.Fatalf("expected visible dataset columns to be allowed: %v", err)
	}
	for _, sql := range []string{
		`SELECT * FROM dataset_abc LIMIT 10`,
		`SELECT _owner_id, col_1 FROM dataset_abc LIMIT 10`,
		`SELECT dataset_abc.* FROM dataset_abc LIMIT 10`,
	} {
		if err := ValidateDatasetOutput(sql); err == nil {
			t.Errorf("expected internal output to be rejected: %s", sql)
		}
	}
}

func stringsHasSuffix(s, suffix string) bool {
	return len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix
}
