package domain

import (
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type FieldDefinition struct {
	Label     string `yaml:"label" json:"label"`
	Format    string `yaml:"format" json:"format,omitempty"`
	Unit      string `yaml:"unit" json:"unit,omitempty"`
	Sensitive bool   `yaml:"sensitive" json:"sensitive,omitempty"`
}

type MetricDefinition struct {
	Label      string   `yaml:"label" json:"label"`
	Definition string   `yaml:"definition" json:"definition"`
	Synonyms   []string `yaml:"synonyms" json:"synonyms,omitempty"`
	Format     string   `yaml:"format" json:"format,omitempty"`
	Unit       string   `yaml:"unit" json:"unit,omitempty"`
}

type Dictionary struct {
	Fields  map[string]FieldDefinition  `yaml:"fields" json:"fields"`
	Aliases map[string]FieldDefinition  `yaml:"aliases" json:"aliases"`
	Metrics map[string]MetricDefinition `yaml:"metrics" json:"metrics"`
}

func LoadDictionary(plugin *Plugin) (*Dictionary, error) {
	dict := &Dictionary{
		Fields:  map[string]FieldDefinition{},
		Aliases: map[string]FieldDefinition{},
		Metrics: map[string]MetricDefinition{},
	}
	if plugin == nil || plugin.DictionaryPath == "" {
		return dict, nil
	}
	data, err := os.ReadFile(plugin.DictionaryPath)
	if err != nil {
		return nil, err
	}
	if err := yaml.Unmarshal(data, dict); err != nil {
		return nil, err
	}
	return dict, nil
}

func (d *Dictionary) Field(column string) FieldDefinition {
	key := strings.ToLower(strings.TrimSpace(column))
	if field, ok := d.Aliases[key]; ok {
		return field
	}
	if field, ok := d.Fields[key]; ok {
		return field
	}
	return FieldDefinition{Label: humanizeColumn(column)}
}

func humanizeColumn(column string) string {
	label := strings.TrimSpace(column)
	label = strings.ReplaceAll(label, "_", " ")
	if label == "" {
		return "未命名字段"
	}
	return label
}
