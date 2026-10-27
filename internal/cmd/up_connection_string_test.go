package cmd

import (
	"testing"

	"github.com/deployado/cli/internal/api"
)

func makeDB() *api.Database {
	return &api.Database{
		DBHost:        "10.0.0.5",
		DBPort:        5432,
		DBUser:        "appuser",
		DBName:        "appdb",
		ConnectionURL: "postgresql://appuser:s3cret@10.0.0.5:5432/appdb",
	}
}

func TestBuildConnectionString_DefaultUsesPlatformURL(t *testing.T) {
	got := buildConnectionString(makeDB(), "")
	want := "postgresql://appuser:s3cret@10.0.0.5:5432/appdb"
	if got != want {
		t.Errorf("default driver: got %q, want %q", got, want)
	}
}

func TestBuildConnectionString_Dotnet(t *testing.T) {
	got := buildConnectionString(makeDB(), "dotnet")
	want := "Host=10.0.0.5;Port=5432;Database=appdb;Username=appuser;Password=s3cret"
	if got != want {
		t.Errorf("dotnet driver: got %q, want %q", got, want)
	}
}

func TestBuildConnectionString_Ecto(t *testing.T) {
	got := buildConnectionString(makeDB(), "ecto")
	want := "ecto://appuser:s3cret@10.0.0.5:5432/appdb"
	if got != want {
		t.Errorf("ecto driver: got %q, want %q", got, want)
	}
}

func TestBuildConnectionString_PG(t *testing.T) {
	got := buildConnectionString(makeDB(), "pg")
	want := "postgres://appuser:s3cret@10.0.0.5:5432/appdb"
	if got != want {
		t.Errorf("pg driver: got %q, want %q", got, want)
	}
}

func TestBuildConnectionString_NoPasswordAvailable(t *testing.T) {
	db := makeDB()
	db.ConnectionURL = ""

	// Default driver falls back to URL without password.
	if got := buildConnectionString(db, ""); got != "postgresql://appuser@10.0.0.5:5432/appdb" {
		t.Errorf("no-pw default: got %q", got)
	}

	// dotnet still emits the field, just empty — better than failing silently.
	if got := buildConnectionString(db, "dotnet"); got != "Host=10.0.0.5;Port=5432;Database=appdb;Username=appuser;Password=" {
		t.Errorf("no-pw dotnet: got %q", got)
	}
}

func TestExtractPasswordFromConnectionURL(t *testing.T) {
	cases := map[string]string{
		"postgresql://u:p@h:5432/d":     "p",
		"postgres://u@h:5432/d":         "",
		"":                              "",
		"not a url":                     "",
		"postgresql://u:p%40word@h/d":   "p@word", // url-encoded password
	}
	for in, want := range cases {
		if got := extractPasswordFromConnectionURL(in); got != want {
			t.Errorf("extractPassword(%q) = %q, want %q", in, got, want)
		}
	}
}
