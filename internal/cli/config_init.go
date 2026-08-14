package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"charm.land/huh/v2"
	"github.com/reconifyhq/reconify/config"
	"github.com/reconifyhq/reconify/engine"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

const optionalHeaderNone = "__reconify_none__"

type configInitPromptFunc func(cmd *cobra.Command, defaultOutPath string) (configInitAnswers, bool, error)

type configInitAnswers struct {
	OutPath string
	Confirm bool

	Timezone string

	Left  configInitSourceAnswers
	Right configInitSourceAnswers

	PairName             string
	DateWindow           string
	AmountToleranceMinor int64
	NameMode             string
}

type configInitSourceAnswers struct {
	Name       string
	FilePath   string
	ParserType string

	DateCol     string
	DateLayout  string
	AmountCol   string
	Decimal     string
	Thousands   string
	Multiplier  int64
	CurrencyCol string
	NameCol     string
	RefCol      string
}

func newConfigInitCmd() *cobra.Command {
	var outPath string
	var force bool

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Interactively create a reconify configuration",
		Long: `Interactively create a reconify.yaml configuration from sample input files.
The wizard reads source headers, asks you to map transaction fields, and writes
a validated Reconify configuration file.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = args
			return runConfigInit(cmd, outPath, force, promptConfigInit)
		},
	}

	cmd.Flags().StringVar(&outPath, "out", "reconify.yaml", "Destination configuration file")
	cmd.Flags().BoolVar(&force, "force", false, "Overwrite the destination configuration file if it exists")

	return cmd
}

func runConfigInit(cmd *cobra.Command, outPath string, force bool, prompt configInitPromptFunc) error {
	outPath = strings.TrimSpace(outPath)
	if outPath == "" {
		outPath = "reconify.yaml"
	}
	if err := ensureCanWriteConfigInit(outPath, force); err != nil {
		return err
	}

	answers, confirmed, err := prompt(cmd, outPath)
	if err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			cmd.PrintErrln("Config init cancelled.")
			return nil
		}
		return err
	}
	if !confirmed || !answers.Confirm {
		cmd.PrintErrln("Config init cancelled.")
		return nil
	}

	answers.OutPath = strings.TrimSpace(answers.OutPath)
	if answers.OutPath == "" {
		answers.OutPath = outPath
	}
	if err := ensureCanWriteConfigInit(answers.OutPath, force); err != nil {
		return err
	}

	cfg, err := buildConfigFromInitAnswers(answers)
	if err != nil {
		return err
	}
	if err := writeConfigInitFile(answers.OutPath, force, cfg); err != nil {
		return err
	}

	cmd.PrintErrf("[OK] wrote %s\n", answers.OutPath)
	cmd.PrintErrf("[OK] %s is valid\n", answers.OutPath)
	return nil
}

func promptConfigInit(cmd *cobra.Command, defaultOutPath string) (configInitAnswers, bool, error) {
	answers := configInitAnswers{
		OutPath:  defaultOutPath,
		Timezone: "UTC",
		Left: configInitSourceAnswers{
			Name: "bank",
		},
		Right: configInitSourceAnswers{
			Name: "stripe",
		},
	}

	if err := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Configuration output path").
				Value(&answers.OutPath).
				Validate(huh.ValidateNotEmpty()),
			huh.NewInput().
				Title("Timezone").
				Description("IANA timezone for generated config defaults").
				Placeholder("UTC").
				Value(&answers.Timezone).
				Validate(huh.ValidateNotEmpty()),
			huh.NewInput().
				Title("Left source name").
				Value(&answers.Left.Name).
				Validate(huh.ValidateNotEmpty()),
			huh.NewInput().
				Title("Left sample file").
				Value(&answers.Left.FilePath).
				Validate(huh.ValidateNotEmpty()),
			huh.NewInput().
				Title("Right source name").
				Value(&answers.Right.Name).
				Validate(huh.ValidateNotEmpty()),
			huh.NewInput().
				Title("Right sample file").
				Value(&answers.Right.FilePath).
				Validate(huh.ValidateNotEmpty()),
		),
	).WithOutput(cmd.ErrOrStderr()).Run(); err != nil {
		return answers, false, err
	}

	answers.Left.Name = strings.TrimSpace(answers.Left.Name)
	answers.Left.FilePath = strings.TrimSpace(answers.Left.FilePath)
	answers.Right.Name = strings.TrimSpace(answers.Right.Name)
	answers.Right.FilePath = strings.TrimSpace(answers.Right.FilePath)
	answers.Timezone = strings.TrimSpace(answers.Timezone)
	answers.OutPath = strings.TrimSpace(answers.OutPath)

	if answers.Left.Name == answers.Right.Name {
		return answers, false, fmt.Errorf("left and right source names must be different")
	}

	leftHeaders, err := readInitHeaders(cmd, answers.Left.FilePath)
	if err != nil {
		return answers, false, fmt.Errorf("read left source headers: %w", err)
	}
	rightHeaders, err := readInitHeaders(cmd, answers.Right.FilePath)
	if err != nil {
		return answers, false, fmt.Errorf("read right source headers: %w", err)
	}

	if err := promptConfigInitSource(cmd, leftHeaders, &answers.Left); err != nil {
		return answers, false, err
	}
	if err := promptConfigInitSource(cmd, rightHeaders, &answers.Right); err != nil {
		return answers, false, err
	}

	amountTolerance := "0"
	answers.PairName = answers.Left.Name + "_vs_" + answers.Right.Name
	answers.DateWindow = "1d"
	answers.NameMode = "tokens"

	if err := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Pair name").
				Value(&answers.PairName).
				Validate(huh.ValidateNotEmpty()),
			huh.NewInput().
				Title("Date window").
				Description("Examples: 0d, 1d, 7d").
				Value(&answers.DateWindow).
				Validate(huh.ValidateNotEmpty()),
			huh.NewInput().
				Title("Amount tolerance in minor units").
				Value(&amountTolerance).
				Validate(validateNonNegativeInt64),
			huh.NewSelect[string]().
				Title("Name matching mode").
				Options(
					huh.NewOption("tokens", "tokens"),
					huh.NewOption("none", "none"),
				).
				Value(&answers.NameMode),
			huh.NewConfirm().
				Title("Write configuration file?").
				Value(&answers.Confirm),
		),
	).WithOutput(cmd.ErrOrStderr()).Run(); err != nil {
		return answers, false, err
	}

	answers.AmountToleranceMinor, err = strconv.ParseInt(strings.TrimSpace(amountTolerance), 10, 64)
	if err != nil {
		return answers, false, fmt.Errorf("amount tolerance must be an integer: %w", err)
	}

	return answers, true, nil
}

func promptConfigInitSource(cmd *cobra.Command, headers []string, source *configInitSourceAnswers) error {
	if len(headers) == 0 {
		return fmt.Errorf("source %q has no readable headers", source.Name)
	}

	source.ParserType = parserTypeForInit(source.FilePath)
	source.DateCol = guessHeader(headers, "date", "posted", "created")
	source.AmountCol = guessHeader(headers, "amount", "value", "total")
	source.CurrencyCol = guessHeader(headers, "currency", "currency_code", "ccy")
	source.NameCol = guessHeader(headers, "description", "details", "name", "memo")
	source.RefCol = guessHeader(headers, "reference", "ref", "id", "transaction_id")
	if source.CurrencyCol == "" {
		source.CurrencyCol = optionalHeaderNone
	}
	if source.NameCol == "" {
		source.NameCol = optionalHeaderNone
	}
	if source.RefCol == "" {
		source.RefCol = optionalHeaderNone
	}
	source.DateLayout = "2006-01-02"
	if usesDelimitedSeparators(source.ParserType) {
		source.Decimal = "."
		source.Thousands = ","
	} else {
		source.Decimal = ""
		source.Thousands = ""
	}
	source.Multiplier = 100
	if source.DateCol == "" {
		source.DateCol = headers[0]
	}
	if source.AmountCol == "" {
		source.AmountCol = headers[0]
	}

	multiplier := strconv.FormatInt(source.Multiplier, 10)
	title := fmt.Sprintf("Map %q fields", source.Name)

	fields := []huh.Field{
		huh.NewSelect[string]().
			Title(title + ": date column").
			Options(headerOptions(headers, false)...).
			Value(&source.DateCol),
		huh.NewSelect[string]().
			Title(title + ": amount column").
			Options(headerOptions(headers, false)...).
			Value(&source.AmountCol),
		huh.NewSelect[string]().
			Title(title + ": currency column").
			Options(headerOptions(headers, true)...).
			Value(&source.CurrencyCol),
		huh.NewSelect[string]().
			Title(title + ": name/description column").
			Options(headerOptions(headers, true)...).
			Value(&source.NameCol),
		huh.NewSelect[string]().
			Title(title + ": reference column").
			Options(headerOptions(headers, true)...).
			Value(&source.RefCol),
		huh.NewInput().
			Title(title + ": date layout").
			Description("Use Go date layout syntax, for example 2006-01-02").
			Value(&source.DateLayout).
			Validate(huh.ValidateNotEmpty()),
	}
	if usesDelimitedSeparators(source.ParserType) {
		fields = append(
			fields,
			huh.NewInput().
				Title(title+": decimal separator").
				Value(&source.Decimal).
				Validate(validateSingleCharOrEmpty),
			huh.NewInput().
				Title(title+": thousands separator").
				Value(&source.Thousands).
				Validate(validateSingleCharOrEmpty),
		)
	}
	fields = append(
		fields,
		huh.NewInput().
			Title(title+": amount multiplier").
			Value(&multiplier).
			Validate(validatePositiveInt64),
	)

	err := huh.NewForm(
		huh.NewGroup(fields...),
	).WithOutput(cmd.ErrOrStderr()).Run()
	if err != nil {
		return err
	}

	parsedMultiplier, err := strconv.ParseInt(strings.TrimSpace(multiplier), 10, 64)
	if err != nil {
		return fmt.Errorf("source %q multiplier must be an integer: %w", source.Name, err)
	}
	source.Multiplier = parsedMultiplier
	source.CurrencyCol = emptyOptionalHeader(source.CurrencyCol)
	source.NameCol = emptyOptionalHeader(source.NameCol)
	source.RefCol = emptyOptionalHeader(source.RefCol)

	return nil
}

func readInitHeaders(cmd *cobra.Command, filePath string) ([]string, error) {
	parserCfg := config.ParserCfg{Type: parserTypeForInit(filePath)}
	headers, err := engine.ReadInputHeaders(cmd.Context(), filePath, parserCfg)
	if err != nil {
		return nil, err
	}

	cleaned := make([]string, 0, len(headers))
	for _, header := range headers {
		header = strings.TrimSpace(header)
		if header != "" {
			cleaned = append(cleaned, header)
		}
	}
	return cleaned, nil
}

func buildConfigFromInitAnswers(answers configInitAnswers) (*config.Config, error) {
	answers.OutPath = strings.TrimSpace(answers.OutPath)
	answers.Timezone = strings.TrimSpace(answers.Timezone)
	answers.PairName = strings.TrimSpace(answers.PairName)
	answers.DateWindow = strings.TrimSpace(answers.DateWindow)
	answers.NameMode = strings.TrimSpace(answers.NameMode)
	answers.Left = normalizeInitSourceAnswers(answers.Left)
	answers.Right = normalizeInitSourceAnswers(answers.Right)

	if answers.Timezone == "" {
		return nil, fmt.Errorf("timezone is required")
	}
	if answers.Left.Name == "" {
		return nil, fmt.Errorf("left source name is required")
	}
	if answers.Right.Name == "" {
		return nil, fmt.Errorf("right source name is required")
	}
	if answers.Left.Name == answers.Right.Name {
		return nil, fmt.Errorf("left and right source names must be different")
	}
	if answers.PairName == "" {
		return nil, fmt.Errorf("pair name is required")
	}
	if answers.DateWindow == "" {
		return nil, fmt.Errorf("date window is required")
	}
	if answers.NameMode == "" {
		answers.NameMode = "none"
	}

	cfg := &config.Config{
		Version:  1,
		Timezone: answers.Timezone,
		Sources: map[string]config.Source{
			answers.Left.Name:  sourceConfigFromInitAnswers(answers.Left, answers.Timezone),
			answers.Right.Name: sourceConfigFromInitAnswers(answers.Right, answers.Timezone),
		},
		Pairs: map[string]config.Pair{
			answers.PairName: {
				Left:                 answers.Left.Name,
				Right:                answers.Right.Name,
				DateWindow:           answers.DateWindow,
				AmountToleranceMinor: answers.AmountToleranceMinor,
				NameMode:             answers.NameMode,
			},
		},
	}

	if errs := cfg.Validate(); len(errs) > 0 {
		return nil, fmt.Errorf("generated config is invalid: %v", errs[0])
	}
	return cfg, nil
}

func normalizeInitSourceAnswers(source configInitSourceAnswers) configInitSourceAnswers {
	source.Name = strings.TrimSpace(source.Name)
	source.FilePath = strings.TrimSpace(source.FilePath)
	source.ParserType = strings.TrimSpace(source.ParserType)
	if source.ParserType == "" {
		source.ParserType = parserTypeForInit(source.FilePath)
	}
	source.DateCol = strings.TrimSpace(source.DateCol)
	source.DateLayout = strings.TrimSpace(source.DateLayout)
	source.AmountCol = strings.TrimSpace(source.AmountCol)
	source.Decimal = strings.TrimSpace(source.Decimal)
	source.Thousands = strings.TrimSpace(source.Thousands)
	if !usesDelimitedSeparators(source.ParserType) {
		source.Decimal = ""
		source.Thousands = ""
	}
	source.CurrencyCol = emptyOptionalHeader(source.CurrencyCol)
	source.NameCol = emptyOptionalHeader(source.NameCol)
	source.RefCol = emptyOptionalHeader(source.RefCol)
	return source
}

func sourceConfigFromInitAnswers(source configInitSourceAnswers, timezone string) config.Source {
	return config.Source{
		FilePattern: source.FilePath,
		Parser: config.ParserCfg{
			Type:        source.ParserType,
			DateCol:     source.DateCol,
			DateLayout:  source.DateLayout,
			TZ:          timezone,
			AmountCol:   source.AmountCol,
			Decimal:     source.Decimal,
			Thousands:   source.Thousands,
			Multiplier:  source.Multiplier,
			CurrencyCol: source.CurrencyCol,
			NameCol:     source.NameCol,
			RefCol:      source.RefCol,
		},
	}
}

func writeConfigInitFile(outPath string, force bool, cfg *config.Config) error {
	if err := ensureCanWriteConfigInit(outPath, force); err != nil {
		return err
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if err := os.WriteFile(outPath, data, 0o600); err != nil {
		return fmt.Errorf("write %q: %w", outPath, err)
	}
	return nil
}

func ensureCanWriteConfigInit(outPath string, force bool) error {
	if strings.TrimSpace(outPath) == "" {
		return fmt.Errorf("--out is required")
	}

	info, err := os.Stat(outPath)
	if err == nil {
		if info.IsDir() {
			return fmt.Errorf("output path %q is a directory", outPath)
		}
		if !force {
			return fmt.Errorf("output path %q already exists; use --force to overwrite", outPath)
		}
		return nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return fmt.Errorf("check output path %q: %w", outPath, err)
}

func parserTypeForInit(filePath string) string {
	switch strings.ToLower(filepath.Ext(filePath)) {
	case ".csv":
		return "csv"
	case ".json", ".ndjson":
		return "json"
	case ".xlsx", ".xlsm":
		return "xlsx"
	default:
		return "auto"
	}
}

func usesDelimitedSeparators(parserType string) bool {
	switch strings.ToLower(strings.TrimSpace(parserType)) {
	case "json", "xlsx":
		return false
	default:
		return true
	}
}

func headerOptions(headers []string, includeNone bool) []huh.Option[string] {
	options := make([]huh.Option[string], 0, len(headers)+1)
	if includeNone {
		options = append(options, huh.NewOption("(none)", optionalHeaderNone))
	}
	for _, header := range headers {
		header = strings.TrimSpace(header)
		if header == "" {
			continue
		}
		options = append(options, huh.NewOption(header, header))
	}
	return options
}

func guessHeader(headers []string, names ...string) string {
	for _, wanted := range names {
		wanted = strings.ToLower(strings.TrimSpace(wanted))
		for _, header := range headers {
			normalized := strings.ToLower(strings.TrimSpace(header))
			if normalized == wanted || strings.Contains(normalized, wanted) {
				return header
			}
		}
	}
	return ""
}

func emptyOptionalHeader(value string) string {
	value = strings.TrimSpace(value)
	if value == optionalHeaderNone {
		return ""
	}
	return value
}

func validateSingleCharOrEmpty(value string) error {
	if len(value) > 1 {
		return fmt.Errorf("must be a single character or empty")
	}
	return nil
}

func validatePositiveInt64(value string) error {
	parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil {
		return fmt.Errorf("must be an integer")
	}
	if parsed <= 0 {
		return fmt.Errorf("must be greater than 0")
	}
	return nil
}

func validateNonNegativeInt64(value string) error {
	parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil {
		return fmt.Errorf("must be an integer")
	}
	if parsed < 0 {
		return fmt.Errorf("must be greater than or equal to 0")
	}
	return nil
}
