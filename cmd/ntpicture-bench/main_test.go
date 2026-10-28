package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func testSettings(t *testing.T) settings {
	t.Helper()
	return settings{output: filepath.Join(t.TempDir(), "report.json"), sink: "discard", mode: "kitty", support: "none", format: "rgba", pattern: "full", policy: "cache", cols: 20, rows: 10, cellWidth: 1, cellHeight: 1, frames: 6, warmup: 3}
}

func TestDiscardReport(t *testing.T) {
	s := testSettings(t)
	if err := run(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(s.output)
	if err != nil {
		t.Fatal(err)
	}
	var r report
	if err = json.Unmarshal(data, &r); err != nil {
		t.Fatal(err)
	}
	if r.Status != "complete" || len(r.Samples) != 6 || r.Warmup.APCBytes == 0 || r.FixtureVersion != 1 || r.Sink != "discard" {
		t.Fatalf("invalid report: %+v", r)
	}
	for _, v := range r.Samples {
		if v.APCBytes == 0 || v.APCBytes >= 40 || v.GridBytes != 0 || v.CompletionNS < v.WriteNS {
			t.Fatalf("warm cache sample: %+v", v)
		}
	}
	if err = run(context.Background(), s); err == nil {
		t.Fatal("overwrote an existing report")
	}
}

func TestCapabilityDeclaration(t *testing.T) {
	s := testSettings(t)
	s.sink = "terminal"
	s.terminal = "example"
	s.version = "test"
	if _, err := s.config(); err == nil {
		t.Fatal("accepted unsupported frame policy")
	}
	s.support = "static"
	if _, err := s.config(); err == nil {
		t.Fatal("accepted frame edits with static-only declaration")
	}
	s.support = "frames"
	if _, err := s.config(); err != nil {
		t.Fatal(err)
	}
	s.policy = "fresh"
	s.support = "static"
	if _, err := s.config(); err != nil {
		t.Fatal(err)
	}
	s.mode = "glyph"
	s.support = "none"
	if _, err := s.config(); err != nil {
		t.Fatal(err)
	}
}

func TestInterruptedReport(t *testing.T) {
	s := testSettings(t)
	s.warmup = 0
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := run(ctx, s); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(s.output)
	if err != nil {
		t.Fatal(err)
	}
	var r report
	if err = json.Unmarshal(data, &r); err != nil {
		t.Fatal(err)
	}
	if r.Status != "interrupted" || len(r.Samples) != 0 || r.P50NS != 0 {
		t.Fatalf("invalid interrupted report: %+v", r)
	}
}
