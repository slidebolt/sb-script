//go:build bdd

// BDD feature tests for sb-script.
// Run: go test -tags bdd -v ./cmd/sb-script/...
package main

import (
	"testing"

	"github.com/cucumber/godog"
)

func TestBDDFeatures(t *testing.T) {
	suite := godog.TestSuite{
		Name: "sb-script-bdd",
		ScenarioInitializer: func(ctx *godog.ScenarioContext) {
			c := newBDDCtx(t)
			c.RegisterSteps(ctx)
		},
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"features"},
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("BDD suite failed")
	}
}
