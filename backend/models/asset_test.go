package models

import (
	"encoding/json"
	"testing"
)

func TestAssetDurationUnmarshal(t *testing.T) {
	cases := []string{
		`{"duration":"0:00:05.123"}`,
		`{"duration":5}`,
		`{"duration":5.5}`,
		`{"duration":null}`,
	}
	for _, in := range cases {
		var a Asset
		if err := json.Unmarshal([]byte(in), &a); err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if in == `{"duration":null}` {
			if a.Duration != "" {
				t.Fatalf("%s: expected empty, got %q", in, a.Duration)
			}
			continue
		}
		if a.Duration == "" {
			t.Fatalf("%s: empty duration", in)
		}
	}
}

func TestFormatSeconds(t *testing.T) {
	if got := formatSeconds(5.0); got != "0:00:05" {
		t.Fatalf("got %q", got)
	}
	if got := formatSeconds(3720.0); got != "1:02:00" {
		t.Fatalf("got %q", got)
	}
}
