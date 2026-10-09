package security

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	dangerousKeywords = regexp.MustCompile(`(?i)\b(DROP|DELETE|TRUNCATE|UPDATE|INSERT|ALTER|CREATE|GRANT|REVOKE|EXEC|EXECUTE|COPY|CALL)\b`)
	dangerousClauses  = regexp.MustCompile(`(?i)\b(INTO|LOCK|VACUUM|ANALYZE|REFRESH|REINDEX|CLUSTER|LISTEN|NOTIFY|UNLISTEN|LOAD)\b|\bFOR\s+(UPDATE|SHARE|NO\s+KEY\s+UPDATE|KEY\s+SHARE)\b`)
	systemSchemas     = regexp.MustCompile(`(?i)\b(pg_catalog|information_schema|pg_toast)\s*\.`)
	dangerousFunction = regexp.MustCompile(`(?i)\b(pg_[a-z0-9_]+|dblink(?:_[a-z0-9_]+)?|lo_import|lo_export|current_setting|set_config)\s*\(`)
	selectPrefix      = regexp.MustCompile(`(?i)^\s*(WITH|SELECT)\b`)
	limitPattern      = regexp.MustCompile(`(?i)\bLIMIT\b`)
	relationPattern   = regexp.MustCompile(`(?i)\b(?:FROM|JOIN)\s+((?:"[^"]+"|[a-zA-Z_][\w$]*)(?:\.(?:"[^"]+"|[a-zA-Z_][\w$]*))?)`)
	ctePattern        = regexp.MustCompile(`(?i)(?:\bWITH|,)\s*([a-zA-Z_][\w$]*)\s+AS\s*\(`)
	internalField     = regexp.MustCompile(`(?i)"?_?(?:owner_id|row_number)"?`)
	selectWildcard    = regexp.MustCompile(`(?i)(?:\bSELECT\s+(?:DISTINCT\s+)?|,)\s*(?:(?:"?[a-zA-Z_][\w$]*"?\.)?\*)\b?`)
)

type ValidationResult struct {
	Valid        bool
	Error        string
	SanitizedSQL string
}

func ValidateDatasetTables(sql, tableName string) error {
	allowed := map[string]bool{strings.ToLower(tableName): true}
	for _, match := range ctePattern.FindAllStringSubmatch(sql, -1) {
		allowed[strings.ToLower(match[1])] = true
	}
	foundDataset := false
	for _, match := range relationPattern.FindAllStringSubmatch(sql, -1) {
		name := strings.ReplaceAll(match[1], `"`, "")
		parts := strings.Split(name, ".")
		name = strings.ToLower(parts[len(parts)-1])
		if !allowed[name] {
			return fmt.Errorf("query references table outside the selected dataset: %s", name)
		}
		if name == strings.ToLower(tableName) {
			foundDataset = true
		}
	}
	if !foundDataset {
		return fmt.Errorf("query does not reference the selected dataset")
	}
	return nil
}

// ValidateDatasetOutput keeps ingestion and row-level security implementation
// details out of model-generated result sets. Imported data tables contain these
// columns internally, but they are never part of a user's dataset schema.
func ValidateDatasetOutput(sql string) error {
	masked, _, _ := inspectSQL(sql)
	if internalField.MatchString(masked) {
		return fmt.Errorf("query selects internal dataset fields")
	}
	if selectWildcard.MatchString(masked) {
		return fmt.Errorf("query uses wildcard columns; select the visible dataset fields explicitly")
	}
	return nil
}

func ValidateSQL(sql string) ValidationResult {
	if strings.TrimSpace(sql) == "" {
		return ValidationResult{Valid: false, Error: "SQL is empty"}
	}
	cleaned := strings.TrimSpace(sql)
	cleaned = strings.TrimSuffix(cleaned, ";")
	masked, hasComment, hasSemicolon := inspectSQL(cleaned)

	if hasComment {
		return ValidationResult{Valid: false, Error: "SQL comments are not allowed"}
	}
	if hasSemicolon {
		return ValidationResult{Valid: false, Error: "Multiple statements are not allowed"}
	}
	if dangerousKeywords.MatchString(masked) || dangerousClauses.MatchString(masked) {
		return ValidationResult{Valid: false, Error: "Dangerous SQL keywords detected"}
	}
	if systemSchemas.MatchString(masked) {
		return ValidationResult{Valid: false, Error: "System schemas are not allowed"}
	}
	if dangerousFunction.MatchString(masked) {
		return ValidationResult{Valid: false, Error: "Dangerous SQL functions detected"}
	}
	if !selectPrefix.MatchString(masked) {
		return ValidationResult{Valid: false, Error: "Only SELECT statements are allowed"}
	}
	// The outer wrapper enforces the row budget even when the model only places
	// LIMIT inside a CTE or nested subquery.
	guarded := fmt.Sprintf("SELECT * FROM (%s) AS agent_guarded_result LIMIT 1000", cleaned)
	return ValidationResult{Valid: true, SanitizedSQL: guarded}
}

func inspectSQL(sql string) (masked string, hasComment, hasSemicolon bool) {
	var out strings.Builder
	inString := false
	for index := 0; index < len(sql); index++ {
		ch := sql[index]
		if inString {
			if ch == '\'' {
				if index+1 < len(sql) && sql[index+1] == '\'' {
					out.WriteString("  ")
					index++
					continue
				}
				inString = false
			}
			out.WriteByte(' ')
			continue
		}
		if ch == '\'' {
			inString = true
			out.WriteByte(' ')
			continue
		}
		if ch == ';' {
			hasSemicolon = true
		}
		if ch == '-' && index+1 < len(sql) && sql[index+1] == '-' {
			hasComment = true
		}
		if ch == '/' && index+1 < len(sql) && sql[index+1] == '*' {
			hasComment = true
		}
		out.WriteByte(ch)
	}
	return out.String(), hasComment, hasSemicolon
}

func MustValidate(sql string) (string, error) {
	r := ValidateSQL(sql)
	if !r.Valid {
		return "", fmt.Errorf("%s", r.Error)
	}
	return r.SanitizedSQL, nil
}
