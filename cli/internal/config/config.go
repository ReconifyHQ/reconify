package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config represents the root configuration structure
type Config struct {
	Version  int               `yaml:"version"`
	Timezone string            `yaml:"timezone"`
	Sources  map[string]Source `yaml:"sources"`
	Pairs    map[string]Pair   `yaml:"pairs"`
}

// Source defines a data source configuration
type Source struct {
	FilePattern string       `yaml:"file_pattern"`
	Parser      CSVParserCfg `yaml:"parser"`
}

// CSVParserCfg defines CSV parsing configuration
type CSVParserCfg struct {
	Type        string `yaml:"type"`
	DateCol     string `yaml:"date_col"`
	DateLayout  string `yaml:"date_layout"`
	TZ          string `yaml:"tz"`
	AmountCol   string `yaml:"amount_col"`
	Decimal     string `yaml:"decimal"`
	Thousands   string `yaml:"thousands"`
	Multiplier  int64  `yaml:"multiplier"`
	CurrencyCol string `yaml:"currency_col,omitempty"`
	NameCol     string `yaml:"name_col,omitempty"`
	RefCol      string `yaml:"ref_col,omitempty"`
}

// Pair defines a reconciliation pair configuration
type Pair struct {
	Left                 string `yaml:"left"`
	Right                string `yaml:"right"`
	DateWindow           string `yaml:"date_window"`
	AmountToleranceMinor int64  `yaml:"amount_tolerance_minor"`
	NameMode             string `yaml:"name_mode"`
}

// Load reads and parses a YAML configuration file
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse YAML: %w", err)
	}

	return &cfg, nil
}

// Validate performs structural validation on the configuration
func (c *Config) Validate() []error {
	var errs []error

	// Validate version
	if c.Version != 1 {
		errs = append(errs, fmt.Errorf("version must be 1 (got %d)", c.Version))
	}

	// Validate timezone
	if c.Timezone != "" {
		if _, err := time.LoadLocation(c.Timezone); err != nil {
			errs = append(errs, fmt.Errorf("timezone %q is invalid: %v", c.Timezone, err))
		}
	}

	// Validate sources
	if len(c.Sources) == 0 {
		errs = append(errs, fmt.Errorf("at least one source is required"))
	}

	for name, source := range c.Sources {
		if sourceErrs := validateSource(name, source); len(sourceErrs) > 0 {
			errs = append(errs, sourceErrs...)
		}
	}

	// Validate pairs
	for name, pair := range c.Pairs {
		if pairErrs := validatePair(name, pair, c.Sources); len(pairErrs) > 0 {
			errs = append(errs, pairErrs...)
		}
	}

	return errs
}

func validateSource(name string, source Source) []error {
	var errs []error

	if source.FilePattern == "" {
		errs = append(errs, fmt.Errorf("sources.%s.file_pattern: required field is missing", name))
	}

	parser := source.Parser
	if parser.Type != "csv" {
		errs = append(errs, fmt.Errorf("sources.%s.parser.type: must be 'csv' (got %q)", name, parser.Type))
	}

	if parser.DateCol == "" {
		errs = append(errs, fmt.Errorf("sources.%s.parser.date_col: required field is missing", name))
	}

	if parser.DateLayout == "" {
		errs = append(errs, fmt.Errorf("sources.%s.parser.date_layout: required field is missing", name))
	}

	if parser.AmountCol == "" {
		errs = append(errs, fmt.Errorf("sources.%s.parser.amount_col: required field is missing", name))
	}

	if len(parser.Decimal) > 1 {
		errs = append(errs, fmt.Errorf("sources.%s.parser.decimal: must be a single character or empty (got %q)", name, parser.Decimal))
	}

	if len(parser.Thousands) > 1 {
		errs = append(errs, fmt.Errorf("sources.%s.parser.thousands: must be a single character or empty (got %q)", name, parser.Thousands))
	}

	if parser.Decimal != "" && parser.Thousands != "" && parser.Decimal == parser.Thousands {
		errs = append(errs, fmt.Errorf("sources.%s.parser.decimal and thousands cannot be the same (both %q)", name, parser.Decimal))
	}

	if parser.Multiplier <= 0 {
		errs = append(errs, fmt.Errorf("sources.%s.parser.multiplier: must be > 0 (got %d)", name, parser.Multiplier))
	}

	if parser.TZ != "" {
		if _, err := time.LoadLocation(parser.TZ); err != nil {
			errs = append(errs, fmt.Errorf("sources.%s.parser.tz: invalid timezone %q: %v", name, parser.TZ, err))
		}
	}

	return errs
}

func validatePair(name string, pair Pair, sources map[string]Source) []error {
	var errs []error

	if pair.Left == "" {
		errs = append(errs, fmt.Errorf("pairs.%s.left: required field is missing", name))
	} else if _, exists := sources[pair.Left]; !exists {
		errs = append(errs, fmt.Errorf("pairs.%s.left: unknown source %q (available: %v)", name, pair.Left, getSourceNames(sources)))
	}

	if pair.Right == "" {
		errs = append(errs, fmt.Errorf("pairs.%s.right: required field is missing", name))
	} else if _, exists := sources[pair.Right]; !exists {
		errs = append(errs, fmt.Errorf("pairs.%s.right: unknown source %q (available: %v)", name, pair.Right, getSourceNames(sources)))
	}

	if pair.Left == pair.Right {
		errs = append(errs, fmt.Errorf("pairs.%s: left and right cannot be the same source", name))
	}

	// Validate date_window format (e.g., "1d", "2d", "7d")
	if pair.DateWindow != "" {
		var days int
		var unit string
		if _, err := fmt.Sscanf(pair.DateWindow, "%d%s", &days, &unit); err != nil {
			errs = append(errs, fmt.Errorf("pairs.%s.date_window: invalid format (expected format like '1d', '2d'): %v", name, err))
		} else if unit != "d" && unit != "D" {
			errs = append(errs, fmt.Errorf("pairs.%s.date_window: unit must be 'd' or 'D' (got %q)", name, unit))
		}
	}

	// Validate amount_tolerance_minor
	if pair.AmountToleranceMinor < 0 {
		errs = append(errs, fmt.Errorf("pairs.%s.amount_tolerance_minor: must be >= 0 (got %d)", name, pair.AmountToleranceMinor))
	}

	// Validate name_mode
	allowedNameModes := map[string]bool{
		"none":   true,
		"tokens": true,
	}
	if pair.NameMode != "" && !allowedNameModes[pair.NameMode] {
		errs = append(errs, fmt.Errorf("pairs.%s.name_mode: must be one of [none, tokens] (got %q)", name, pair.NameMode))
	}

	return errs
}

func getSourceNames(sources map[string]Source) []string {
	names := make([]string, 0, len(sources))
	for name := range sources {
		names = append(names, name)
	}
	return names
}
