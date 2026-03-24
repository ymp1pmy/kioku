package config

import (
	"os"
	"path/filepath"
)

type Config struct {
	DataDir    string
	DBPath     string
	ModelsDir  string
	ModelName  string
}

func Load() (*Config, error) {
	base, err := dataDir()
	if err != nil {
		return nil, err
	}

	modelsDir := filepath.Join(base, "models")
	if err := os.MkdirAll(modelsDir, 0755); err != nil {
		return nil, err
	}

	modelName := os.Getenv("KIOKU_EMBED_MODEL")
	if modelName == "" {
		modelName = "KnightsAnalytics/all-MiniLM-L6-v2"
	}

	return &Config{
		DataDir:   base,
		DBPath:    filepath.Join(base, "kioku.db"),
		ModelsDir: modelsDir,
		ModelName: modelName,
	}, nil
}

func dataDir() (string, error) {
	if d := os.Getenv("KIOKU_DATA_DIR"); d != "" {
		if err := os.MkdirAll(d, 0755); err != nil {
			return "", err
		}
		return d, nil
	}

	xdg := os.Getenv("XDG_DATA_HOME")
	if xdg == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		xdg = filepath.Join(home, ".local", "share")
	}

	d := filepath.Join(xdg, "kioku")
	if err := os.MkdirAll(d, 0755); err != nil {
		return "", err
	}
	return d, nil
}
