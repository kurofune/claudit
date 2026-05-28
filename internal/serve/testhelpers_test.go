package serve

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// chtimes sets both atime and mtime to t. Tests use it to force a
// future mtime so cache invalidation logic runs deterministically on
// filesystems with coarse mtime resolution.
func chtimes(path string, t time.Time) error {
	return os.Chtimes(path, t, t)
}

// mkAssistantLine renders one assistant JSONL line with the given
// identifiers and timestamp. Shared by the serve handler/event tests.
func mkAssistantLine(uuid, parent string, ts time.Time) string {
	return fmt.Sprintf(`{"type":"assistant","uuid":%q,"parentUuid":%q,"timestamp":%q,"sessionId":"s1","cwd":"/p","message":{"model":"claude-opus-4-7","role":"assistant","content":[{"type":"text","text":"x"}],"usage":{"input_tokens":100,"output_tokens":200,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":0}}}}`,
		uuid, parent, ts.Format(time.RFC3339))
}

// writeJSONL writes the given lines (newline-terminated) to path,
// creating parent directories as needed.
func writeJSONL(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	body := ""
	for _, l := range lines {
		body += l + "\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
