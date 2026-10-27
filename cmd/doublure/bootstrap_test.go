package main

import (
	"regexp"
	"testing"
)

func TestDefaultAppName(t *testing.T) {
	t.Parallel()

	re := regexp.MustCompile(`^doublure-dev-[0-9a-f]{6}$`)
	name := defaultAppName()
	if !re.MatchString(name) {
		t.Fatalf("defaultAppName() = %q, want match %s", name, re)
	}
	if flag := bootstrapAppCmd().Flags().Lookup("name"); !re.MatchString(flag.DefValue) {
		t.Fatalf("--name default = %q, want match %s", flag.DefValue, re)
	}
}
