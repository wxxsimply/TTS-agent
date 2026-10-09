package fewshot

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Example struct {
	Question string   `json:"question"`
	SQL      string   `json:"sql"`
	Tags     []string `json:"tags"`
}

type Retriever struct {
	examples []Example
}

func NewRetriever(fewshotDir string) (*Retriever, error) {
	path := filepath.Join(fewshotDir, "examples.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return &Retriever{}, nil
	}
	var examples []Example
	if err := json.Unmarshal(data, &examples); err != nil {
		return nil, err
	}
	return &Retriever{examples: examples}, nil
}

func (r *Retriever) Retrieve(query string, topK int) []Example {
	if len(r.examples) == 0 {
		return nil
	}
	type scored struct {
		ex    Example
		score float64
	}
	queryTokens := tokenize(query)
	df := map[string]int{}
	for _, ex := range r.examples {
		seen := map[string]bool{}
		for _, tok := range tokenize(ex.Question) {
			if !seen[tok] {
				df[tok]++
				seen[tok] = true
			}
		}
	}
	N := float64(len(r.examples))
	var scores []scored
	for _, ex := range r.examples {
		docTokens := tokenize(ex.Question)
		score := bm25Score(queryTokens, docTokens, N, df, 1.5, 0.75)
		if score > 0 {
			scores = append(scores, scored{ex, score})
		}
	}
	sort.Slice(scores, func(i, j int) bool { return scores[i].score > scores[j].score })
	if len(scores) > topK {
		scores = scores[:topK]
	}
	out := make([]Example, len(scores))
	for i, s := range scores {
		out[i] = s.ex
	}
	return out
}

func tokenize(text string) []string {
	runes := []rune(strings.ToLower(text))
	var tokens []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			tokens = append(tokens, string(cur))
			cur = nil
		}
	}
	for _, r := range runes {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || (r >= 0x4e00 && r <= 0x9fff) {
			cur = append(cur, r)
		} else {
			flush()
		}
	}
	flush()
	return tokens
}

func bm25Score(query, doc []string, N float64, df map[string]int, k1, b float64) float64 {
	docLen := float64(len(doc))
	tf := map[string]int{}
	for _, t := range doc {
		tf[t]++
	}
	avgDL := docLen
	if avgDL == 0 {
		return 0
	}
	var score float64
	for _, qt := range query {
		freq := float64(tf[qt])
		if freq == 0 {
			continue
		}
		idf := math.Log((N-float64(df[qt])+0.5)/(float64(df[qt])+0.5)+1)
		denom := freq + k1*(1-b+b*docLen/avgDL)
		score += idf * (freq * (k1 + 1)) / denom
	}
	return score
}
