package conf

import "testing"

// TestDBSpecs checks that RPEEK_DB_<ALIAS> variables are collected as alias→DSN pairs with
// the alias lower-cased, and that a variable lacking the exact prefix, carrying an empty
// alias, or holding an empty value is ignored.
func TestDBSpecs(t *testing.T) {
	// A DSN may itself contain '=' in its query string; the whole value must be preserved.
	const appDSN = "postgres://u:p@localhost/app?sslmode=require"

	t.Setenv("RPEEK_DB_APP", appDSN)
	t.Setenv("RPEEK_DB_Analytics", "mysql://u:p@localhost/an") // mixed-case suffix lower-cases
	t.Setenv("RPEEK_DB_EMPTY", "")                             // empty value: skipped
	t.Setenv("RPEEK_DB_", "orphan")                            // empty alias: skipped
	t.Setenv("RPEEK_DBX", "notadb")                            // the prefix requires the underscore
	t.Setenv("RPEEK_HOST", "example.com")                      // unrelated setting

	specs := DBSpecs()

	if got := specs["app"]; got != appDSN {
		t.Errorf(`specs["app"] = %q, want %q`, got, appDSN)
	}
	if got := specs["analytics"]; got != "mysql://u:p@localhost/an" {
		t.Errorf(`specs["analytics"] = %q, want the alias lower-cased`, got)
	}
	// "x" and "host" guard against an over-broad prefix (RPEEK_DB / RPEEK_) matching.
	for _, absent := range []string{"empty", "", "x", "host"} {
		if got, ok := specs[absent]; ok {
			t.Errorf("specs[%q] = %q, want it to be absent", absent, got)
		}
	}
}
