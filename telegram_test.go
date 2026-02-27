package main

import (
	"strings"
	"testing"
)

func TestTelegramIsAllowed(t *testing.T) {
	tg := &Telegram{
		AllowedIDs: []int64{123, 456},
	}

	if !tg.IsAllowed(123) {
		t.Error("expected 123 to be allowed")
	}
	if !tg.IsAllowed(456) {
		t.Error("expected 456 to be allowed")
	}
	if tg.IsAllowed(789) {
		t.Error("expected 789 to be denied")
	}
}

func TestTelegramFormatHTML(t *testing.T) {
	input := "Hello **world** and `code` and ```block```"
	output := formatHTML(input)

	if !strings.Contains(output, "<b>world</b>") {
		t.Errorf("expected <b>world</b>, got %s", output)
	}
	if !strings.Contains(output, "<code>code</code>") {
		t.Errorf("expected <code>code</code>, got %s", output)
	}
	if !strings.Contains(output, "<pre>block</pre>") {
		t.Errorf("expected <pre>block</pre>, got %s", output)
	}
}
