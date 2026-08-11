package e2e_test

import (
	"testing"

	"ails-hpc/test/e2e"
)

func TestE2E_FullSuite(t *testing.T) {
	e2e.RunAllE2ETests(t)
}
