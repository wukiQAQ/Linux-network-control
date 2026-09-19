package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestCaptureReadersParsing 验证 capture.readers 的解析：正常值生效，非法值忽略（保持默认 1）。
func TestCaptureReadersParsing(t *testing.T) {
	dir := t.TempDir()

	path := filepath.Join(dir, "readers.toml")
	if err := os.WriteFile(path, []byte("[capture]\niface = \"eth0\"\nreaders = 4\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Readers != 4 {
		t.Errorf("Readers=%d, want 4", cfg.Readers)
	}

	bad := filepath.Join(dir, "bad.toml")
	if err := os.WriteFile(bad, []byte("[capture]\nreaders = 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg2, err := Load(bad)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg2.Readers != 1 {
		t.Errorf("非法值应回退默认 1，实际 %d", cfg2.Readers)
	}

	if Default().Readers != 1 {
		t.Errorf("默认 Readers 应为 1，实际 %d", Default().Readers)
	}
}
