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

func TestConfigValidate_DateWindowNegative(t *testing.T) {
	cfg := baseValidConfig()
	cfg.Pairs["p"] = Pair{
		Left:                 "left",
		Right:                "right",
		DateWindow:           "-1d",
		AmountToleranceMinor: 0,
		NameMode:             "none",
	}

	errs := cfg.Validate()
	if len(errs) == 0 {
		t.Fatal("expected validation error for negative date_window")
	}
}

func TestConfigValidate_IndexSpillDirRejectsParentTraversal(t *testing.T) {
	cfg := baseValidConfig()
	cfg.Index = IndexCfg{Backend: "disk", SpillDir: "../tmp"}

	errs := cfg.Validate()
	if len(errs) == 0 {
		t.Fatal("expected validation error for spill_dir containing '..'")

	}
}

func TestConfigValidate_ParserTypes(t *testing.T) {
	for _, parserType := range []string{"", "auto", "csv", "json", "xlsx"} {
		t.Run(parserType, func(t *testing.T) {
			cfg := baseValidConfig()
			left := cfg.Sources["left"]
			left.Parser.Type = parserType
			cfg.Sources["left"] = left
			if errs := cfg.Validate(); len(errs) > 0 {
				t.Fatalf("expected valid parser type %q, got errors: %v", parserType, errs)
			}
		})
	}
}

func TestConfigValidate_ParserTypeInvalid(t *testing.T) {
	cfg := baseValidConfig()
	left := cfg.Sources["left"]
	left.Parser.Type = "xml"
	cfg.Sources["left"] = left

	errs := cfg.Validate()
	if len(errs) == 0 {
		t.Fatal("expected validation error for invalid parser type")
	}
}

func TestConfigValidate_PassesOmitted_PreservesLegacyBehavior(t *testing.T) {
	cfg := baseValidConfig()
	p := cfg.Pairs["p"]
	p.NameMode = "tokens"
	p.NameMatchThreshold = 0.6
	cfg.Pairs["p"] = p

	if errs := cfg.Validate(); len(errs) > 0 {
		t.Fatalf("expected valid legacy config without passes, got errors: %v", errs)
	}
}

func TestConfigValidate_PassesExplicit_Valid(t *testing.T) {
	cfg := baseValidConfig()
	p := cfg.Pairs["p"]
	p.Passes = []PassConfig{
		{Type: PassTypeReferenceOneToOne},
		{Type: PassTypeNameTokensOneToOne},
	}
	cfg.Pairs["p"] = p

	if errs := cfg.Validate(); len(errs) > 0 {
		t.Fatalf("expected valid explicit passes config, got errors: %v", errs)
	}
}

func TestConfigValidate_PassesUnknownType(t *testing.T) {
	cfg := baseValidConfig()
	p := cfg.Pairs["p"]
	p.Passes = []PassConfig{
		{Type: PassTypeReferenceOneToOne},
		{Type: "fuzzy_match"},
	}
	cfg.Pairs["p"] = p

	errs := cfg.Validate()
	if len(errs) == 0 {
		t.Fatal("expected validation error for unknown pass type")
	}
	found := false
	for _, err := range errs {
		if strings.Contains(err.Error(), "passes[1].type") && strings.Contains(err.Error(), "fuzzy_match") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected error identifying passes[1].type, got: %v", errs)
	}
}

func TestConfigValidate_PassesEmpty_IsRejected(t *testing.T) {
	cfg := baseValidConfig()
	p := cfg.Pairs["p"]
	p.Passes = []PassConfig{}
	cfg.Pairs["p"] = p

	errs := cfg.Validate()
	if len(errs) == 0 {
		t.Fatal("expected validation error for empty passes list")
	}
}

func TestConfigValidate_PassesWithNameModeTokens_IsRejected(t *testing.T) {
	cfg := baseValidConfig()
	p := cfg.Pairs["p"]
	p.NameMode = "tokens"
	p.Passes = []PassConfig{
		{Type: PassTypeReferenceOneToOne},
	}
	cfg.Pairs["p"] = p

	errs := cfg.Validate()
	if len(errs) == 0 {
		t.Fatal("expected validation error when name_mode=tokens is combined with passes")
	}
	found := false
	for _, err := range errs {
		if strings.Contains(err.Error(), "name_mode=tokens") && strings.Contains(err.Error(), PassTypeNameTokensOneToOne) {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected migration-oriented error about name_mode=tokens, got: %v", errs)
	}
}

func TestConfigValidate_SupportedPassTypes_AreValid(t *testing.T) {
	for _, passType := range []string{PassTypeReferenceOneToOne, PassTypeNameTokensOneToOne} {
		t.Run(passType, func(t *testing.T) {
			cfg := baseValidConfig()
			p := cfg.Pairs["p"]
			p.Passes = []PassConfig{{Type: passType}}
			cfg.Pairs["p"] = p

			if errs := cfg.Validate(); len(errs) > 0 {
				t.Fatalf("expected pass type %q to be valid, got errors: %v", passType, errs)
			}
		})
	}
}

func TestConfigValidate_OneToManyPass_IsRejected(t *testing.T) {
	cfg := baseValidConfig()
	p := cfg.Pairs["p"]
	p.Passes = []PassConfig{{Type: "one_to_many"}}
	cfg.Pairs["p"] = p

	errs := cfg.Validate()
	if len(errs) == 0 {
		t.Fatal("expected validation error for unsupported one_to_many pass")
	}
	found := false
	for _, err := range errs {
		if strings.Contains(err.Error(), "one_to_many") && strings.Contains(err.Error(), "not supported yet") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected one_to_many not-supported error, got: %v", errs)
	}
}

func TestConfigValidate_PassesCompatibleWithRights(t *testing.T) {
	cfg := withLedgerSource(baseValidConfig())
	p := cfg.Pairs["p"]
	p.Right = ""
	p.Rights = []string{"right", "ledger"}
	p.Passes = []PassConfig{{Type: PassTypeReferenceOneToOne}}
	cfg.Pairs["p"] = p

	if errs := cfg.Validate(); len(errs) > 0 {
		t.Fatalf("expected passes to be compatible with rights, got errors: %v", errs)
	}
}
