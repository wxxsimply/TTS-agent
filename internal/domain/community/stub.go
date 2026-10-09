package community

import "github.com/tts-agent/internal/domain"

func Seed(root, dsn string) error {
	_ = root
	_ = dsn
	return nil
}

func InitRegistry(root string) {
	domain.Register(&domain.Plugin{
		Name:         "community",
		DDLPath:      domain.ResolvePath(root, "community", "ddl.sql"),
		MetadataPath: domain.ResolvePath(root, "community", "metadata.yaml"),
		FewshotDir:   domain.ResolvePath(root, "community", "fewshot"),
		SeedFn:       Seed,
	})
}
