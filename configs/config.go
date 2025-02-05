package configs

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"github.com/spf13/pflag"
	"os"
	"strings"
)

var (
	//go:embed language_extensions.json
	LangExtensions []byte

	ValidOrderBy = map[string]struct{}{
		"lines":   {},
		"commits": {},
		"files":   {},
	}

	ValidFormats = map[string]struct{}{
		"tabular":    {},
		"csv":        {},
		"json":       {},
		"json-lines": {},
	}
)

type Config struct {
	UseCommitter bool
	RepoPath     string
	Revision     string
	OrderBy      string
	Format       string
	Extensions   []string
	Languages    []string
	Excludes     []string
	RestrictTo   []string
}

type LanguageExtension struct {
	Name       string   `json:"name"`
	Type       string   `json:"type"`
	Extensions []string `json:"extensions"`
}

// ParseConfig gets gitfame parameters from program arguments, validates them and wraps into Config
func ParseConfig() (Config, error) {
	var config Config
	pflag.StringVar(&config.RepoPath, "repository", ".", "Path to the git repository")
	pflag.StringVar(&config.Revision, "revision", "HEAD", "Git revision to analyze")
	pflag.StringVar(&config.OrderBy, "order-by", "lines", "Key to sort results by: lines, commits, files")
	pflag.BoolVar(&config.UseCommitter, "use-committer", false, "Use committer instead of author for calculations")
	pflag.StringVar(&config.Format, "format", "tabular", "Output format: tabular, csv, json, json-lines")
	pflag.StringSliceVar(&config.Extensions, "extensions", nil, "Comma-separated list of file extensions to include")
	pflag.StringSliceVar(&config.Languages, "languages", nil, "Comma-separated list of languages to include")
	pflag.StringSliceVar(&config.Excludes, "exclude", nil, "Comma-separated list of Glob patterns to exclude files")
	pflag.StringSliceVar(&config.RestrictTo, "restrict-to", nil, "Comma-separated list of Glob patterns to restrict files")
	pflag.Parse()

	if _, ok := ValidFormats[config.Format]; !ok {
		return Config{}, fmt.Errorf("invalid format: %s", config.Format)
	}

	if _, ok := ValidOrderBy[config.OrderBy]; !ok {
		return Config{}, fmt.Errorf("invalid order-by: %s", config.OrderBy)
	}

	languagesMap := make(map[string]struct{})
	for _, lang := range config.Languages {
		languagesMap[strings.ToLower(lang)] = struct{}{}
	}

	if err := config.loadLanguageExtensions(languagesMap); err != nil {
		return Config{}, err
	}

	return config, nil
}

// loadLanguageExtensions converts the language parameters into extensions list via language_extensions.json
func (cfg *Config) loadLanguageExtensions(languagesMap map[string]struct{}) error {
	if len(languagesMap) == 0 {
		return nil
	}

	var extensions []LanguageExtension
	err := json.Unmarshal(LangExtensions, &extensions)
	if err != nil {
		return fmt.Errorf("failed to unmarshal language extensions: %v", err)
	}

	for _, extension := range extensions {
		lang := strings.ToLower(extension.Name)
		if _, ok := languagesMap[lang]; ok {
			cfg.Extensions = append(cfg.Extensions, extension.Extensions...)
			delete(languagesMap, lang)
		}
	}

	if len(languagesMap) != 0 {
		if _, err = fmt.Fprintf(os.Stderr, "Warning: undefined languages: %s\n", func() string {
			builder := strings.Builder{}
			for lang := range languagesMap {
				builder.WriteString(", ")
				builder.WriteString(lang)
			}
			return strings.TrimPrefix(builder.String(), ", ")
		}()); err != nil {
			return fmt.Errorf("failed write warning in stderr: %w", err)
		}
	}

	return nil
}
