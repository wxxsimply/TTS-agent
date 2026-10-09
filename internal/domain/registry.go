package domain

import (
	"fmt"
	"os"
	"path/filepath"
)

type Plugin struct {
	Name           string
	DDLPath        string
	MetadataPath   string
	DictionaryPath string
	FewshotDir     string
	SeedFn         func(root string, dsn string) error
}

var registry = map[string]*Plugin{}

func Register(p *Plugin) {
	registry[p.Name] = p
}

func Get(name string) (*Plugin, error) {
	p, ok := registry[name]
	if !ok {
		return nil, fmt.Errorf("domain %q not registered", name)
	}
	return p, nil
}

func Default() (*Plugin, error) {
	return Get("ecommerce")
}

func ReadDDL(p *Plugin) (string, error) {
	b, err := os.ReadFile(p.DDLPath)
	return string(b), err
}

func ResolvePath(root string, parts ...string) string {
	all := append([]string{root}, parts...)
	return filepath.Join(all...)
}
