package config

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

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

func TestCSVParserCfg_GroupColPassthrough(t *testing.T) {
	yamlSrc := `
type: csv
date_col: date
date_layout: "2006-01-02"
amount_col: amount
ref_col: txn_id
group_col: invoice_number
`
	var cfg CSVParserCfg
	if err := yaml.Unmarshal([]byte(yamlSrc), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if cfg.GroupCol != "invoice_number" {
		t.Errorf("GroupCol = %q, want %q", cfg.GroupCol, "invoice_number")
	}
	if cfg.RefCol != "txn_id" {
		t.Errorf("RefCol = %q, want %q", cfg.RefCol, "txn_id")
	}
}

func TestPair_Counterparts(t *testing.T) {
	tests := []struct {
		name string
		pair Pair
		want []string
	}{
		{name: "right only", pair: Pair{Right: "stripe"}, want: []string{"stripe"}},
		{name: "rights only", pair: Pair{Rights: []string{"stripe", "paypal"}}, want: []string{"stripe", "paypal"}},
		{name: "neither set", pair: Pair{}, want: nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.pair.Counterparts()
			if len(got) != len(tc.want) {
				t.Fatalf("Counterparts() = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("Counterparts() = %v, want %v", got, tc.want)
				}
			}
		})
	}
}

func withLedgerSource(cfg Config) Config {
	cfg.Sources["ledger"] = Source{
		FilePattern: "ledger.csv",
		Parser: CSVParserCfg{
			Type:       "csv",
			DateCol:    "date",
			DateLayout: "2006-01-02",
			AmountCol:  "amount",
			Multiplier: 100,
		},
	}
	return cfg
}

func TestConfigValidate_RightAndRightsMutuallyExclusive(t *testing.T) {
	cfg := withLedgerSource(baseValidConfig())
	p := cfg.Pairs["p"]
	p.Rights = []string{"right", "ledger"}
	cfg.Pairs["p"] = p

	errs := cfg.Validate()
	if len(errs) == 0 {
		t.Fatal("expected validation error when both right and rights are set")
	}
	found := false
	for _, err := range errs {
		if strings.Contains(err.Error(), "mutually exclusive") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a mutually-exclusive error, got: %v", errs)
	}
}

func TestConfigValidate_RightsRequiredWhenRightUnset(t *testing.T) {
	cfg := baseValidConfig()
	p := cfg.Pairs["p"]
	p.Right = ""
	cfg.Pairs["p"] = p

	errs := cfg.Validate()
	if len(errs) == 0 {
		t.Fatal("expected validation error when neither right nor rights is set")
	}
}

func TestConfigValidate_RightsValid(t *testing.T) {
	cfg := withLedgerSource(baseValidConfig())
	p := cfg.Pairs["p"]
	p.Right = ""
	p.Rights = []string{"right", "ledger"}
	cfg.Pairs["p"] = p

	if errs := cfg.Validate(); len(errs) > 0 {
		t.Fatalf("expected valid multi-counterpart config, got errors: %v", errs)
	}
}

func TestConfigValidate_NameMatchThresholdBounds(t *testing.T) {
	tests := []struct {
		name      string
		threshold float64
		wantErr   bool
	}{
		{name: "unset defaults fine", threshold: 0, wantErr: false},
		{name: "valid mid-range", threshold: 0.5, wantErr: false},
		{name: "valid just below upper bound", threshold: 0.999, wantErr: false},
		{name: "negative", threshold: -0.1, wantErr: true},
		{name: "above 1", threshold: 1.1, wantErr: true},
		{name: "exactly 1 is unreachable under strict >", threshold: 1, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := baseValidConfig()
			p := cfg.Pairs["p"]
			p.NameMatchThreshold = tc.threshold
			cfg.Pairs["p"] = p

			errs := cfg.Validate()
			if tc.wantErr && len(errs) == 0 {
				t.Fatal("expected validation error, got none")
			}
			if !tc.wantErr && len(errs) > 0 {
				t.Fatalf("expected no validation error, got: %v", errs)
			}
		})
	}
}
