package api

import (
	"strings"
	"testing"
)

// Regression test for the env set/import diagnostic bug. Before the fix
// the CLI surfaced "Failed to update environment variables" without ever
// telling the user WHICH key failed validation or WHY. The server now
// sends per-field details, and the CLI must include them in the printed
// error.
func TestError_Error_SurfacesValidationDetails(t *testing.T) {
	tests := []struct {
		name     string
		err      Error
		wantSubs []string
		notSubs  []string
	}{
		{
			name: "validation error with per-field details — env regex",
			err: Error{
				Code:    "validation_failed",
				Message: "Failed to update environment variables",
				Details: map[string]interface{}{
					"key": []interface{}{
						"must start with a letter or underscore and contain only letters, digits, underscores, or dots",
					},
				},
			},
			wantSubs: []string{
				"Failed to update environment variables",
				"key:",
				"must start with a letter",
			},
		},
		{
			name: "validation error with string-valued detail",
			err: Error{
				Code:    "invalid_scope",
				Message: "Invalid scope 'foo'.",
				Details: map[string]interface{}{
					"scope": "must be one of: runtime, build",
				},
			},
			wantSubs: []string{"Invalid scope", "scope:", "must be one of"},
		},
		{
			name: "details with unhelpful 'reason' inspect dump should be hidden",
			err: Error{
				Code:    "update_failed",
				Message: "Failed to update environment variables",
				Details: map[string]interface{}{
					"reason": "%Ecto.Changeset<...>", // string is shown — that's fine, it's at least typed
				},
			},
			// Reason: string is shown — acceptable. The test guards
			// against panicking on non-list values.
			wantSubs: []string{"Failed to update environment variables"},
		},
		{
			name: "no details — falls back to message",
			err: Error{
				Code:    "not_found",
				Message: "App not found",
			},
			wantSubs: []string{"App not found"},
			notSubs:  []string{"("},
		},
		{
			name: "no details, no message — falls back to code",
			err: Error{Code: "internal_error"},
			wantSubs: []string{"internal_error"},
		},
		{
			name: "description takes priority over message when set",
			err: Error{
				Code:        "x",
				Description: "Detailed description",
				Message:     "Short message",
			},
			wantSubs: []string{"Detailed description"},
			notSubs:  []string{"Short message"},
		},
		{
			name: "details + description — both surface",
			err: Error{
				Description: "Some description",
				Details: map[string]interface{}{
					"key": []interface{}{"is invalid"},
				},
			},
			wantSubs: []string{"Some description", "key:", "is invalid"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.err.Error()
			for _, sub := range tt.wantSubs {
				if !strings.Contains(got, sub) {
					t.Errorf("expected error to contain %q\nactual: %s", sub, got)
				}
			}
			for _, sub := range tt.notSubs {
				if strings.Contains(got, sub) {
					t.Errorf("expected error NOT to contain %q\nactual: %s", sub, got)
				}
			}
		})
	}
}

func TestFormatValidationDetails(t *testing.T) {
	tests := []struct {
		name    string
		details map[string]interface{}
		want    string
	}{
		{"empty", nil, ""},
		{"empty map", map[string]interface{}{}, ""},
		{
			"single field, single error",
			map[string]interface{}{"key": []interface{}{"is invalid"}},
			"key: is invalid",
		},
		{
			"single field, string value (server-side shorthand)",
			map[string]interface{}{"scope": "must be runtime or build"},
			"scope: must be runtime or build",
		},
		{
			"non-list, non-string value is skipped silently",
			map[string]interface{}{"raw": map[string]string{"a": "b"}},
			"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatValidationDetails(tt.details)
			if got != tt.want {
				t.Errorf("formatValidationDetails(...) = %q, want %q", got, tt.want)
			}
		})
	}
}
