package config

import "testing"

func baseValidConfig() Config {
	return Config{
		Version:  1,
		Timezone: "UTC",
		Sources: map[string]Source{
			"left": {
				FilePattern: "left.csv",
				Parser: CSVParserCfg{
					Type:       "csv",
					DateCol:    "date",
					DateLayout: "2006-01-02",
					AmountCol:  "amount",
					Multiplier: 100,
				},
			},
			"right": {
				FilePattern: "right.csv",
				Parser: CSVParserCfg{
					Type:       "csv",
					DateCol:    "date",
					DateLayout: "2006-01-02",
					AmountCol:  "amount",
					Multiplier: 100,
				},
			},
		},
		Pairs: map[string]Pair{
			"p": {
				Left:                 "left",
				Right:                "right",
				DateWindow:           "0d",
				AmountToleranceMinor: 0,
				NameMode:             "none",
			},
		},
	}
}

func TestConfigValidate_IndexBackend(t *testing.T) {
	cfg := baseValidConfig()
	cfg.Index = IndexCfg{Backend: "disk"}

	if errs := cfg.Validate(); len(errs) > 0 {
		t.Fatalf("expected valid config, got errors: %v", errs)
	}
}

func TestConfigValidate_IndexBackendInvalid(t *testing.T) {
	cfg := baseValidConfig()
	cfg.Index = IndexCfg{Backend: "ramdisk"}

	errs := cfg.Validate()
	if len(errs) == 0 {
		t.Fatal("expected validation error for invalid index.backend")
	}
}

func TestConfigValidate_IndexAutoMaxRightFileMBNegative(t *testing.T) {
	cfg := baseValidConfig()
	cfg.Index = IndexCfg{Backend: "auto", AutoMaxRightFileMB: -1}

	errs := cfg.Validate()
	if len(errs) == 0 {
		t.Fatal("expected validation error for negative auto_max_right_file_mb")
	}
}
