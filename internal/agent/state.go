package agent

import (
	"github.com/google/uuid"

	"github.com/tts-agent/internal/domain"
	"github.com/tts-agent/internal/fewshot"
	"github.com/tts-agent/internal/schema"
)

// State contains the mutable data shared by nodes during one agent run.
type State struct {
	RunID             uuid.UUID
	GraphVersion      string
	ConversationID    uuid.UUID
	DatasetID         uuid.UUID
	Domain            string
	CurrentNode       NodeID
	NextNode          NodeID
	StepSequence      int
	JobID             uuid.UUID
	JobAttempt        int
	WorkerID          string
	Query             string
	Schemas           []schema.SchemaResult
	Examples          []fewshot.Example
	SQL               string
	Rows              []map[string]interface{}
	Errors            []string
	Status            string
	Attempts          int
	SeenSQL           map[string]struct{}
	DatasetTimeHint   string
	DatasetTable      string
	UserID            uuid.UUID
	Dictionary        *domain.Dictionary
	Report            Report
	ChartConfig       map[string]interface{}
	Steps             []StepTrace
	InputTokens       int64
	OutputTokens      int64
	LLMCalls          int
	EstimatedCostYuan float64
	PromptVersion     string
	RunSource         string
}
