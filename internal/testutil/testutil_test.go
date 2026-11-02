package testutil

import (
	"strings"
	"testing"
)

func TestTestDatabaseURLRefusesNonTestDatabases(t *testing.T) {
	for _, url := range []string{
		"postgres://u:p@localhost:5432/crm",
		"postgres://u:p@localhost:5432/crm_testing",
		"postgres://u:p@localhost:5432/test_crm",
	} {
		t.Setenv("TEST_DATABASE_URL", url)
		if _, err := TestDatabaseURL(); err == nil || !strings.Contains(err.Error(), "refusing") {
			t.Errorf("%s: err = %v, want refusal", url, err)
		}
	}

	t.Setenv("TEST_DATABASE_URL", "postgres://u:p@localhost:5432/crm_test")
	if _, err := TestDatabaseURL(); err != nil {
		t.Errorf("crm_test refused: %v", err)
	}
}
