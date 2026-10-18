package app

// The brand input has to reach the wizard from the CALLER, not just from an internal
// variable. brand_assets.go documents SetBrandToken as "Called from flag parsing", but no
// flag was registered, so the documented seam had no input: a white-label build could not
// pass its brand at all. This test pins the wiring.

import (
	"flag"
	"os"
	"testing"
)

func TestBrandFlagIsRegistered(t *testing.T) {
	f := flag.Lookup("brand")
	if f == nil {
		t.Fatalf("no --brand flag registered: SetBrandToken is documented as called from flag parsing but nothing calls it")
	}
	if f.DefValue != "" {
		t.Errorf("--brand default = %q, want empty so the unbranded path stays byte-identical", f.DefValue)
	}
}

func TestBrandFlagSetsTheProcessBrand(t *testing.T) {
	prev := brandEnvValue
	t.Cleanup(func() { SetBrandToken(prev) })

	if err := applyBrandFlag("net4sats"); err != nil {
		t.Fatalf("applyBrandFlag(net4sats) = %v", err)
	}
	if brandEnvValue != "net4sats" {
		t.Errorf("brandEnvValue = %q, want net4sats", brandEnvValue)
	}
	if brandTokenHint != "net4sats" {
		t.Errorf("brandTokenHint = %q, want net4sats (the logo accessible name reads this)", brandTokenHint)
	}
}

func TestBrandFlagRefusesAnUnsafeToken(t *testing.T) {
	prev := brandEnvValue
	t.Cleanup(func() { SetBrandToken(prev) })

	// A token load_brand() would coerce away must NOT look accepted here, or the caller
	// believes it branded a router that silently kept the default.
	if err := applyBrandFlag("not a brand!"); err == nil {
		t.Errorf("applyBrandFlag accepted an unsafe token; want a refusal")
	}
}

func TestBrandFlagExplicitDefaultIsAllowed(t *testing.T) {
	prev := brandEnvValue
	t.Cleanup(func() { SetBrandToken(prev) })

	// "--brand tollgate" is a legitimate way to say "no brand" and must not be an error.
	if err := applyBrandFlag("tollgate"); err != nil {
		t.Errorf("applyBrandFlag(tollgate) = %v, want nil (explicitly the default)", err)
	}
	if brandEnvValue != "" {
		t.Errorf("brandEnvValue = %q, want empty for the explicit default", brandEnvValue)
	}
}

func TestBrandEnvFallback(t *testing.T) {
	prev := brandEnvValue
	t.Cleanup(func() { SetBrandToken(prev); os.Unsetenv("TOLLGATE_BRAND") })

	os.Setenv("TOLLGATE_BRAND", "net4sats")
	if err := applyBrandFlag(""); err != nil {
		t.Fatalf("applyBrandFlag with an empty token and TOLLGATE_BRAND set = %v", err)
	}
	if brandEnvValue != "net4sats" {
		t.Errorf("brandEnvValue = %q, want net4sats from the env fallback", brandEnvValue)
	}
}
