package releasehistory

import (
	"strings"
	"testing"
)

const (
	commitA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	commitB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	commitC = "cccccccccccccccccccccccccccccccccccccccc"
	objectA = "dddddddddddddddddddddddddddddddddddddddd"
	objectB = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
)

func TestStableAnnotatedTagsUsesOnlyUnambiguousFullStableSemVerTags(t *testing.T) {
	input := strings.Join([]string{
		objectB + "\trefs/tags/v1.10.0",
		commitB + "\trefs/tags/v1.10.0^{}",
		objectA + "\trefs/tags/v1.2.0",
		commitA + "\trefs/tags/v1.2.0^{}",
		commitC + "\trefs/tags/v2.0.0", // lightweight: ignored
		objectA + "\trefs/tags/v3.0.0-rc.1",
		commitA + "\trefs/tags/v3.0.0-rc.1^{}",
		objectA + "\trefs/tags/v3.0.0+build",
		commitA + "\trefs/tags/v3.0.0+build^{}",
		objectA + "\trefs/tags/v01.0.0",
		commitA + "\trefs/tags/v01.0.0^{}",
	}, "\n") + "\n"

	got, err := StableAnnotatedTags(strings.NewReader(input), 100)
	if err != nil {
		t.Fatalf("StableAnnotatedTags() error = %v", err)
	}
	want := []Tag{
		{Name: "v1.2.0", Object: objectA, Commit: commitA},
		{Name: "v1.10.0", Object: objectB, Commit: commitB},
	}
	if len(got) != len(want) {
		t.Fatalf("StableAnnotatedTags() len = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("StableAnnotatedTags()[%d] = %#v, want %#v", i, got[i], want[i])
		}
	}
}

func TestStableAnnotatedTagsFailsClosed(t *testing.T) {
	tests := []struct {
		name  string
		input string
		max   int
	}{
		{
			name:  "malformed response",
			input: "not-a-ls-remote-record\n",
			max:   100,
		},
		{
			name: "ambiguous direct object",
			input: strings.Join([]string{
				objectA + "\trefs/tags/v1.0.0",
				objectB + "\trefs/tags/v1.0.0",
				commitA + "\trefs/tags/v1.0.0^{}",
			}, "\n") + "\n",
			max: 100,
		},
		{
			name: "candidate bound",
			input: strings.Join([]string{
				objectA + "\trefs/tags/v1.0.0",
				commitA + "\trefs/tags/v1.0.0^{}",
				objectB + "\trefs/tags/v2.0.0",
				commitB + "\trefs/tags/v2.0.0^{}",
			}, "\n") + "\n",
			max: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := StableAnnotatedTags(strings.NewReader(tt.input), tt.max); err == nil {
				t.Fatal("StableAnnotatedTags() error = nil, want fail-closed error")
			}
		})
	}
}

func TestHighestPreviousUsesRealSemVerOrdering(t *testing.T) {
	input := "v1.2.0\t" + objectA + "\t" + commitA + "\n" +
		"v1.10.0\t" + objectB + "\t" + commitB + "\n"
	got, ok, err := HighestPrevious(strings.NewReader(input), "v2.0.0")
	if err != nil {
		t.Fatalf("HighestPrevious() error = %v", err)
	}
	if !ok {
		t.Fatal("HighestPrevious() ok = false, want true")
	}
	want := Tag{Name: "v1.10.0", Object: objectB, Commit: commitB}
	if got != want {
		t.Fatalf("HighestPrevious() = %#v, want %#v", got, want)
	}
}

func TestHighestPreviousRejectsNonIncreasingOrInvalidCurrent(t *testing.T) {
	tests := []struct {
		name    string
		current string
		input   string
	}{
		{name: "equal", current: "v1.2.0", input: "v1.2.0\t" + objectA + "\t" + commitA + "\n"},
		{name: "lower", current: "v1.2.0", input: "v1.3.0\t" + objectA + "\t" + commitA + "\n"},
		{name: "prerelease current", current: "v1.3.0-rc.1", input: "v1.2.0\t" + objectA + "\t" + commitA + "\n"},
		{name: "build current", current: "v1.3.0+build", input: "v1.2.0\t" + objectA + "\t" + commitA + "\n"},
		{name: "malformed published record", current: "v2.0.0", input: "v1.2.0\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, err := HighestPrevious(strings.NewReader(tt.input), tt.current); err == nil {
				t.Fatal("HighestPrevious() error = nil, want fail-closed error")
			}
		})
	}
}
