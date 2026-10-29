package play

import (
	"os"
	"path/filepath"
	"testing"
)

func TestChunkSizeDefault(t *testing.T) {
	if DefaultChunkSize != 8<<20 {
		t.Fatalf("default chunk %d", DefaultChunkSize)
	}
	c, err := New(t.Context(), Options{StaticToken: "test-token"})
	if err != nil {
		t.Fatal(err)
	}
	if c.ChunkSize() != DefaultChunkSize {
		t.Fatalf("%d", c.ChunkSize())
	}
	c, err = New(t.Context(), Options{StaticToken: "test-token", ChunkSize: 256 * 1024})
	if err != nil {
		t.Fatal(err)
	}
	if c.ChunkSize() != 256*1024 {
		t.Fatalf("%d", c.ChunkSize())
	}
}

func TestCheckSize(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.aab")
	if err := os.WriteFile(path, []byte("ab"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CheckSize(path, "AAB", MaxBundleBytes); err != nil {
		t.Fatal(err)
	}
	if err := CheckSize(path, "AAB", 1); err == nil {
		t.Fatal("expected size rejection")
	}
	if err := CheckSize(filepath.Join(dir, "missing.aab"), "AAB", MaxBundleBytes); err == nil {
		t.Fatal("expected missing file")
	}
	if MaxBundleBytes != 50<<30 || MaxMappingBytes != 2000<<20 {
		t.Fatalf("limits %d %d", MaxBundleBytes, MaxMappingBytes)
	}
}
