package pgutil

import "testing"

func TestParseURLDSNAndRewriteDatabase(t *testing.T) {
	info, err := parseDSN("postgres://postgres:Postgres%40123456@CHANGE_ME_HOST:30432/adk?sslmode=disable")
	if err != nil {
		t.Fatalf("parseDSN() error = %v", err)
	}
	if info.database != "adk" {
		t.Fatalf("database = %q, want adk", info.database)
	}
	admin, err := info.withDatabase("postgres")
	if err != nil {
		t.Fatalf("withDatabase() error = %v", err)
	}
	if admin != "postgres://postgres:Postgres%40123456@CHANGE_ME_HOST:30432/postgres?sslmode=disable" {
		t.Fatalf("admin DSN = %q", admin)
	}
}

func TestParseKeywordValueDSNAndRewriteDatabase(t *testing.T) {
	info, err := parseDSN("host=CHANGE_ME_HOST port=30432 user=postgres password=Postgres@123456 dbname=adk sslmode=disable")
	if err != nil {
		t.Fatalf("parseDSN() error = %v", err)
	}
	if info.database != "adk" {
		t.Fatalf("database = %q, want adk", info.database)
	}
	admin, err := info.withDatabase("postgres")
	if err != nil {
		t.Fatalf("withDatabase() error = %v", err)
	}
	want := "host=CHANGE_ME_HOST port=30432 user=postgres password=Postgres@123456 dbname=postgres sslmode=disable"
	if admin != want {
		t.Fatalf("admin DSN = %q, want %q", admin, want)
	}
}

func TestQuoteIdentifier(t *testing.T) {
	if got := quoteIdentifier(`ad"k`); got != `"ad""k"` {
		t.Fatalf("quoteIdentifier() = %q", got)
	}
}
