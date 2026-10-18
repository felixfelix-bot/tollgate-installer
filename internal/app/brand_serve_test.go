package app

// The brand has to reach the SERVED page, not just a variable. brand_assets.go offered
// brandedHTML, but handleIndex wrote indexHTML raw, so the function had no caller and the
// asset slot was dead code: passing --brand changed nothing an operator could see.

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleIndexAppliesTheBrand(t *testing.T) {
	prev := brandEnvValue
	t.Cleanup(func() { SetBrandToken(prev) })

	SetBrandToken("net4sats")
	rec := httptest.NewRecorder()
	handleIndex(rec, httptest.NewRequest("GET", "/", nil))
	body := rec.Body.String()

	if !strings.Contains(body, "Net4sats") {
		t.Errorf("served page does not carry the brand display spelling %q; the brand did not reach handleIndex", "Net4sats")
	}
	// The DISPLAY spelling is what a user reads; the bare token is a build input and is
	// not required to appear. (Asserting the token here would pin an implementation
	// detail rather than the contract that load_brand() defines.)
	if strings.Contains(body, "TollGate Installer — Router Setup") {
		t.Errorf("served page still titles itself TollGate with a brand set")
	}
}

func TestHandleIndexUnbrandedIsByteIdentical(t *testing.T) {
	prev := brandEnvValue
	t.Cleanup(func() { SetBrandToken(prev) })

	SetBrandToken("")
	rec := httptest.NewRecorder()
	handleIndex(rec, httptest.NewRequest("GET", "/", nil))
	if got := rec.Body.String(); got != string(indexHTML) {
		t.Errorf("an unbranded wizard must serve indexHTML byte for byte; got a %d-byte page, want %d", len(got), len(indexHTML))
	}
}
