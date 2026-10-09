package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/tts-agent/internal/agent"
	"github.com/tts-agent/internal/config"
	"github.com/tts-agent/internal/domain/community"
	"github.com/tts-agent/internal/domain/ecommerce"
	"github.com/tts-agent/internal/llm"
	"github.com/tts-agent/internal/schema"
	"github.com/tts-agent/internal/security"
	"github.com/tts-agent/internal/store"
)

type benchmarkCase struct {
	ID             int      `json:"id"`
	Question       string   `json:"question"`
	Domain         string   `json:"domain,omitempty"`
	ExpectedTables []string `json:"expected_tables"`
	RequiredSQL    []string `json:"required_sql,omitempty"`
	ForbiddenSQL   []string `json:"forbidden_sql,omitempty"`
}

type caseResult struct {
	ID              int      `json:"id"`
	Question        string   `json:"question"`
	SQLValid        bool     `json:"sql_valid"`
	TableMatch      bool     `json:"table_match"`
	SemanticMatch   bool     `json:"semantic_match"`
	Executed        bool     `json:"executed"`
	Status          string   `json:"status"`
	ErrorType       string   `json:"error_type,omitempty"`
	DurationMS      int64    `json:"duration_ms"`
	GeneratedTables []string `json:"generated_tables,omitempty"`
}

type evaluationReport struct {
	Version           string       `json:"version"`
	PromptVersion     string       `json:"prompt_version"`
	GeneratedAt       time.Time    `json:"generated_at"`
	Total             int          `json:"total"`
	SQLValidRate      float64      `json:"sql_valid_rate"`
	TableMatchRate    float64      `json:"table_match_rate"`
	SemanticMatchRate float64      `json:"semantic_match_rate"`
	ExecutionRate     float64      `json:"execution_rate"`
	AverageDurationMS float64      `json:"average_duration_ms"`
	RegressionPoints  float64      `json:"regression_points,omitempty"`
	GatePassed        bool         `json:"gate_passed"`
	GateFailures      []string     `json:"gate_failures,omitempty"`
	Results           []caseResult `json:"results"`
}

var relationPattern = regexp.MustCompile(`(?i)\b(?:FROM|JOIN)\s+([a-zA-Z_][\w$]*)`)

func main() {
	datasetPath := flag.String("dataset", "tests/eval/benchmark.json", "benchmark JSON path")
	outputPath := flag.String("output", "", "write JSON report to this path")
	baselinePath := flag.String("baseline", "", "optional baseline report")
	limit := flag.Int("limit", 0, "run only the first N cases")
	minSQLValid := flag.Float64("min-sql-valid", 100, "minimum SQL safety rate")
	minTableMatch := flag.Float64("min-table-match", 85, "minimum expected table match rate")
	minExecution := flag.Float64("min-execution", 85, "minimum execution success rate")
	maxRegression := flag.Float64("max-regression", 3, "maximum execution-rate regression in percentage points")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		fatal(err)
	}
	ecommerce.InitRegistry(cfg.DomainsPath)
	community.InitRegistry(cfg.DomainsPath)

	cases := loadCases(*datasetPath)
	if *limit > 0 && *limit < len(cases) {
		cases = cases[:*limit]
	}
	if len(cases) == 0 {
		fatal(fmt.Errorf("benchmark has no cases"))
	}

	ctx := context.Background()
	db, err := store.NewPostgres(ctx, cfg.DatabaseURL, cfg.DatabaseReadonlyURL)
	if err != nil {
		fatal(err)
	}
	defer db.Close()
	llmClient := llm.NewClient(cfg.DeepSeekAPIKey, cfg.DeepSeekBaseURL, cfg.DeepSeekModel, cfg.LLMTimeoutSec).
		WithPricing(cfg.LLMInputPricePerMillion, cfg.LLMOutputPricePerMillion)
	pipeline := agent.NewPipeline(db, llmClient, schema.NewRouter(db, cfg.EmbeddingDimension, cfg.DomainsPath), cfg.MaxAgentRetries, cfg.StatementTimeoutMS)

	report := evaluate(ctx, pipeline, cases)
	report.GatePassed = true
	if report.SQLValidRate < *minSQLValid {
		report.GateFailures = append(report.GateFailures, fmt.Sprintf("SQL valid rate %.2f < %.2f", report.SQLValidRate, *minSQLValid))
	}
	if report.TableMatchRate < *minTableMatch {
		report.GateFailures = append(report.GateFailures, fmt.Sprintf("table match rate %.2f < %.2f", report.TableMatchRate, *minTableMatch))
	}
	if report.ExecutionRate < *minExecution {
		report.GateFailures = append(report.GateFailures, fmt.Sprintf("execution rate %.2f < %.2f", report.ExecutionRate, *minExecution))
	}
	if *baselinePath != "" {
		baseline := loadReport(*baselinePath)
		report.RegressionPoints = baseline.ExecutionRate - report.ExecutionRate
		if report.RegressionPoints > *maxRegression {
			report.GateFailures = append(report.GateFailures, fmt.Sprintf("execution regression %.2f > %.2f points", report.RegressionPoints, *maxRegression))
		}
	}
	report.GatePassed = len(report.GateFailures) == 0

	encoded, _ := json.MarshalIndent(report, "", "  ")
	fmt.Println(string(encoded))
	if *outputPath != "" {
		if err := os.MkdirAll(filepath.Dir(*outputPath), 0o755); err != nil && filepath.Dir(*outputPath) != "." {
			fatal(err)
		}
		if err := os.WriteFile(*outputPath, encoded, 0o644); err != nil {
			fatal(err)
		}
	}
	if !report.GatePassed {
		os.Exit(2)
	}
}

func evaluate(ctx context.Context, pipeline *agent.Pipeline, cases []benchmarkCase) evaluationReport {
	report := evaluationReport{Version: agent.CurrentGraphVersion, PromptVersion: agent.CurrentPromptVersion, GeneratedAt: time.Now().UTC(), Results: make([]caseResult, 0, len(cases))}
	validCount, tableCount, semanticCount, executionCount := 0, 0, 0, 0
	var durationTotal int64
	for _, item := range cases {
		domainName := item.Domain
		if domainName == "" {
			domainName = "ecommerce"
		}
		result, runErr := pipeline.Run(ctx, agent.Input{Query: item.Question, Domain: domainName, RunSource: "evaluation"})
		if result == nil {
			result = &agent.Result{Status: agent.RunFailed, ErrorType: "runtime_error"}
		}
		validation := security.ValidateSQL(result.SQL)
		tableSet := map[string]bool{}
		for _, match := range relationPattern.FindAllStringSubmatch(result.SQL, -1) {
			tableSet[strings.ToLower(match[1])] = true
		}
		generatedTables := make([]string, 0, len(tableSet))
		for name := range tableSet {
			generatedTables = append(generatedTables, name)
		}
		tableMatch := containsAll(tableSet, item.ExpectedTables)
		semanticMatch := containsFragments(result.SQL, item.RequiredSQL, item.ForbiddenSQL)
		executed := runErr == nil && result.Status == agent.RunCompleted
		entry := caseResult{ID: item.ID, Question: item.Question, SQLValid: validation.Valid, TableMatch: tableMatch, SemanticMatch: semanticMatch, Executed: executed, Status: result.Status, ErrorType: result.ErrorType, DurationMS: result.DurationMS, GeneratedTables: generatedTables}
		report.Results = append(report.Results, entry)
		if validation.Valid {
			validCount++
		}
		if tableMatch {
			tableCount++
		}
		if semanticMatch {
			semanticCount++
		}
		if executed {
			executionCount++
		}
		durationTotal += result.DurationMS
	}
	report.Total = len(cases)
	report.SQLValidRate = rate(validCount, report.Total)
	report.TableMatchRate = rate(tableCount, report.Total)
	report.SemanticMatchRate = rate(semanticCount, report.Total)
	report.ExecutionRate = rate(executionCount, report.Total)
	report.AverageDurationMS = float64(durationTotal) / float64(report.Total)
	return report
}

func containsAll(actual map[string]bool, expected []string) bool {
	for _, name := range expected {
		if !actual[strings.ToLower(name)] {
			return false
		}
	}
	return true
}

func containsFragments(sql string, required, forbidden []string) bool {
	normalized := strings.ToLower(sql)
	for _, fragment := range required {
		if !strings.Contains(normalized, strings.ToLower(fragment)) {
			return false
		}
	}
	for _, fragment := range forbidden {
		if strings.Contains(normalized, strings.ToLower(fragment)) {
			return false
		}
	}
	return true
}

func rate(count, total int) float64 { return float64(count) / float64(total) * 100 }

func loadCases(path string) []benchmarkCase {
	data, err := os.ReadFile(path)
	if err != nil {
		fatal(err)
	}
	var cases []benchmarkCase
	if err := json.Unmarshal(data, &cases); err != nil {
		fatal(err)
	}
	return cases
}

func loadReport(path string) evaluationReport {
	data, err := os.ReadFile(path)
	if err != nil {
		fatal(err)
	}
	var report evaluationReport
	if err := json.Unmarshal(data, &report); err != nil {
		fatal(err)
	}
	return report
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
