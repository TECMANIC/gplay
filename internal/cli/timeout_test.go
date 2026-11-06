package cli

import (
	"testing"
	"time"
)

func TestMediaTimeout(t *testing.T) {
	if mediaTimeout("", "") != 60*time.Second {
		t.Fatal("a release with no media uses the short deadline")
	}
	if mediaTimeout("app.aab", "") != 30*time.Minute {
		t.Fatal("an AAB upload uses the media deadline")
	}
	if mediaTimeout("", "mapping.txt") != 30*time.Minute {
		t.Fatal("a mapping upload uses the media deadline")
	}
}
