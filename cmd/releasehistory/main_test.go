package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunCandidatesEmitsLiteralStableAnnotatedRecords(t *testing.T) {
	input := strings.Join([]string{
		"dddddddddddddddddddddddddddddddddddddddd\trefs/tags/v1.10.0",
		"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\trefs/tags/v1.10.0^{}",
		"eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee\trefs/tags/v1.2.0",
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\trefs/tags/v1.2.0^{}",
	}, "\n") + "\n"
	var stdout bytes.Buffer

	if err := run([]string{"candidates", "--max", "100"}, strings.NewReader(input), &stdout); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	want := "v1.2.0\teeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee\taaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n" +
		"v1.10.0\tdddddddddddddddddddddddddddddddddddddddd\tbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\n"
	if stdout.String() != want {
		t.Fatalf("run() output = %q, want %q", stdout.String(), want)
	}
}

func TestRunPreviousUsesLiteralCurrentVersion(t *testing.T) {
	input := "v1.2.0\teeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee\taaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n" +
		"v1.10.0\tdddddddddddddddddddddddddddddddddddddddd\tbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\n"
	var stdout bytes.Buffer

	if err := run([]string{"previous", "--current", "v2.0.0"}, strings.NewReader(input), &stdout); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	want := "v1.10.0\tdddddddddddddddddddddddddddddddddddddddd\tbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\n"
	if stdout.String() != want {
		t.Fatalf("run() output = %q, want %q", stdout.String(), want)
	}
}

func TestRunRejectsMissingOrUnknownArguments(t *testing.T) {
	tests := [][]string{
		nil,
		{"candidates"},
		{"candidates", "--max", "0"},
		{"previous"},
		{"unknown"},
	}
	for _, args := range tests {
		if err := run(args, strings.NewReader(""), &bytes.Buffer{}); err == nil {
			t.Errorf("run(%q) error = nil, want error", args)
		}
	}
}
