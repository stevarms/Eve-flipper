package sde

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureSDEExtractedQuarantinesIncompleteExtract(t *testing.T) {
	dataDir := t.TempDir()
	zipPath := filepath.Join(dataDir, "sde.zip")
	extractDir := filepath.Join(dataDir, "sde")

	if err := os.MkdirAll(extractDir, 0755); err != nil {
		t.Fatalf("create incomplete extract: %v", err)
	}
	if err := os.WriteFile(filepath.Join(extractDir, "partial.txt"), []byte("broken"), 0644); err != nil {
		t.Fatalf("write partial marker: %v", err)
	}
	if err := writeMinimalSDEZip(zipPath); err != nil {
		t.Fatalf("write sde zip: %v", err)
	}

	if err := ensureSDEExtracted(dataDir, zipPath, extractDir); err != nil {
		t.Fatalf("ensure SDE extracted: %v", err)
	}
	if err := validateSDEExtractDir(extractDir); err != nil {
		t.Fatalf("validate activated extract: %v", err)
	}
	if _, err := os.Stat(filepath.Join(extractDir, "partial.txt")); !os.IsNotExist(err) {
		t.Fatalf("partial marker still exists in activated extract: %v", err)
	}

	entries, err := os.ReadDir(dataDir)
	if err != nil {
		t.Fatalf("read data dir: %v", err)
	}
	foundQuarantine := false
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "sde.corrupt.") {
			foundQuarantine = true
			break
		}
	}
	if !foundQuarantine {
		t.Fatalf("expected incomplete extract to be quarantined")
	}
}

func TestValidateSDEExtractDirRequiresCoreFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "mapRegions.jsonl"), []byte("{}\n"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := validateSDEExtractDir(dir); err == nil {
		t.Fatalf("validate SDE extract succeeded with missing required files")
	}
}

func writeMinimalSDEZip(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	zw := zip.NewWriter(f)
	for _, baseName := range requiredSDEJSONLFiles {
		w, err := zw.Create(baseName + ".jsonl")
		if err != nil {
			zw.Close()
			return err
		}
		if _, err := w.Write([]byte("{}\n")); err != nil {
			zw.Close()
			return err
		}
	}
	return zw.Close()
}

func TestMarketGroupPath(t *testing.T) {
	d := &Data{MarketGroups: map[int32]*MarketGroup{
		11:  {ID: 11, Name: "Ammunition & Charges"},
		100: {ID: 100, Name: "Projectile Ammo", ParentID: 11},
		200: {ID: 200, Name: "Advanced", ParentID: 100},
		7:   {ID: 7, Name: "Loop A", ParentID: 8},
		8:   {ID: 8, Name: "Loop B", ParentID: 7},
	}}
	got := d.MarketGroupPath(200)
	want := []string{"Ammunition & Charges", "Projectile Ammo", "Advanced"}
	if len(got) != len(want) {
		t.Fatalf("path = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("path = %v, want %v", got, want)
		}
	}
	if p := d.MarketGroupPath(999); len(p) != 0 {
		t.Fatalf("unknown group gave %v", p)
	}
	if p := d.MarketGroupPath(7); len(p) != 2 {
		t.Fatalf("a cycle must stop after visiting each group once, got %v", p)
	}
}
