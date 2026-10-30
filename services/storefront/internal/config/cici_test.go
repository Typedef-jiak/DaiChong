package config

import "testing"

func TestCiciConfigurationDefaultsAndRehearsal(t *testing.T) {
	for _, name := range []string{"MIGRATION_REHEARSAL", "CICI_ENABLED", "CICI_PURCHASES_ENABLED", "CICI_API_KEY", "CICI_API_KEY_FILE", "CICI_API_SECRET", "CICI_API_SECRET_FILE"} {
		t.Setenv(name, "")
	}
	s, err := LoadCici()
	if err != nil || s.Client != nil || s.PurchasesEnabled {
		t.Fatal("live integration enabled by default")
	}
	t.Setenv("CICI_ENABLED", "true")
	if _, err = LoadCici(); err == nil {
		t.Fatal("missing key accepted")
	}
	t.Setenv("CICI_API_KEY", "fixture-key")
	t.Setenv("CICI_API_SECRET", "fixture-secret")
	s, err = LoadCici()
	if err != nil || s.Client == nil || s.PurchasesEnabled {
		t.Fatal("read-only config invalid", err)
	}
	t.Setenv("CICI_PURCHASES_ENABLED", "true")
	s, err = LoadCici()
	if err != nil || !s.PurchasesEnabled {
		t.Fatal("explicit purchasing not enabled", err)
	}
	t.Setenv("MIGRATION_REHEARSAL", "true")
	s, err = LoadCici()
	if err != nil || s.Client != nil || s.PurchasesEnabled {
		t.Fatal("rehearsal permits live Cici calls", err)
	}
}
