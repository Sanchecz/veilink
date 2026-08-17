package logging

import (
	"bytes"
	"strings"
	"testing"

	"veilink/internal/config"
)

func TestNewFormatsAndLevels(t *testing.T) {
	for _, format := range []string{"json", "text"} {
		var b bytes.Buffer
		logger, err := New(config.Log{Level: "debug", Format: format}, &b)
		if err != nil {
			t.Fatal(err)
		}
		logger.Debug("hello", "key", "value")
		if !strings.Contains(b.String(), "hello") {
			t.Fatalf("%s logger wrote %q", format, b.String())
		}
	}
	if _, err := New(config.Log{Level: "trace", Format: "json"}, &bytes.Buffer{}); err == nil {
		t.Fatal("invalid level accepted")
	}
	if _, err := New(config.Log{Level: "info", Format: "xml"}, &bytes.Buffer{}); err == nil {
		t.Fatal("invalid format accepted")
	}
}
