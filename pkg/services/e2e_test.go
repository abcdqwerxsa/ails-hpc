package services_test

import (
	"testing"

	"ails-hpc/test/e2e"
)

// E2E Test Suite integration entrypoint under pkg/services

func TestServices_E2E_Suite(t *testing.T) {
	e2e.RunAllE2ETests(t)
}
