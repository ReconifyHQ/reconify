package cli

import "testing"

func TestGetConfigPath_Default(t *testing.T) {
	t.Setenv("RECONIFY_CONFIG", "")
	configFile = "reconify.yaml"
	configExplicit = false

	if got := getConfigPath(); got != "reconify.yaml" {
		t.Fatalf("getConfigPath() = %q, want %q", got, "reconify.yaml")
	}
}

func TestGetConfigPath_EnvFallback(t *testing.T) {
	t.Setenv("RECONIFY_CONFIG", "env.yaml")
	configFile = "reconify.yaml"
	configExplicit = false

	if got := getConfigPath(); got != "env.yaml" {
		t.Fatalf("getConfigPath() = %q, want %q", got, "env.yaml")
	}
}

func TestGetConfigPath_ExplicitFlagWinsOverEnv(t *testing.T) {
	t.Setenv("RECONIFY_CONFIG", "env.yaml")
	configFile = "flag.yaml"
	configExplicit = true

	if got := getConfigPath(); got != "flag.yaml" {
		t.Fatalf("getConfigPath() = %q, want %q", got, "flag.yaml")
	}
}
