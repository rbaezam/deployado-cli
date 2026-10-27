package cmd

import "testing"

func TestMatchesDatabaseDeleteConfirmation(t *testing.T) {
	t.Run("matches logical name", func(t *testing.T) {
		if !matchesDatabaseDeleteConfirmation("laundryflow-db", "laundryflow-db", "shirtless_devs_laundryflow_db") {
			t.Fatal("expected logical name to match")
		}
	})

	t.Run("matches physical db name", func(t *testing.T) {
		if !matchesDatabaseDeleteConfirmation("shirtless_devs_laundryflow_db", "laundryflow-db", "shirtless_devs_laundryflow_db") {
			t.Fatal("expected db_name to match")
		}
	})

	t.Run("trims spaces", func(t *testing.T) {
		if !matchesDatabaseDeleteConfirmation("  laundryflow-db  ", "laundryflow-db", "shirtless_devs_laundryflow_db") {
			t.Fatal("expected trimmed value to match")
		}
	})

	t.Run("rejects unrelated value", func(t *testing.T) {
		if matchesDatabaseDeleteConfirmation("other", "laundryflow-db", "shirtless_devs_laundryflow_db") {
			t.Fatal("expected unrelated value to fail")
		}
	})
}
