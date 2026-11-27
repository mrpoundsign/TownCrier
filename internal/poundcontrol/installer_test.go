package poundcontrol_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mrpoundsign/TownCrier/internal/poundcontrol"
)

func TestIsPoundControlInstalledAndTestIsolation(t *testing.T) {
	if poundcontrol.IsPoundControlInstalled("") {
		t.Fatal("expected IsPoundControlInstalled(\"\") to return false during unit tests")
	}
	if poundcontrol.IsPluginInstalled("") {
		t.Fatal("expected IsPluginInstalled(\"\") to return false during unit tests")
	}
	if _, err := poundcontrol.InstallWasmFile("dummy.wasm", "", "0.1.0"); err == nil {
		t.Fatal("expected InstallWasmFile with empty targetWasmDir to fail during unit tests")
	}

	root := t.TempDir()
	wasmDir := filepath.Join(root, "PoundControl", "wasm")
	if poundcontrol.IsPoundControlInstalled(wasmDir) {
		t.Fatal("expected false before PoundControl directory exists")
	}

	// Creating parent config dir is enough to detect PoundControl.
	if err := os.MkdirAll(filepath.Dir(wasmDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if !poundcontrol.IsPoundControlInstalled(wasmDir) {
		t.Fatal("expected true when PoundControl parent directory exists")
	}
}

func TestInstallWasmFilePreservesMetadataAndRemovesLegacy(t *testing.T) {
	root := t.TempDir()
	wasmDir := filepath.Join(root, "PoundControl", "wasm")
	if err := os.MkdirAll(wasmDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Pre-populate .installed.json with another plugin (Twitch) and a legacy towncrier.wasm.
	existingMeta := `{
  "plugins": {
    "com.github.mrpoundsign.poundcontrol.twitch": {
      "uuid": "com.github.mrpoundsign.poundcontrol.twitch",
      "filename": "com.github.mrpoundsign.poundcontrol.twitch.wasm",
      "source": "github",
      "repository": "https://github.com/mrpoundsign/PoundControl-Twitch",
      "assetName": "poundcontrol-twitch.wasm",
      "installedVersion": "0.1.0"
    }
  }
}`
	if err := os.WriteFile(filepath.Join(wasmDir, ".installed.json"), []byte(existingMeta), 0o600); err != nil {
		t.Fatal(err)
	}
	legacyPath := filepath.Join(wasmDir, "towncrier.wasm")
	if err := os.WriteFile(legacyPath, []byte("legacy-wasm"), 0o600); err != nil {
		t.Fatal(err)
	}

	srcDir := filepath.Join(root, "exe")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	srcWasm := filepath.Join(srcDir, poundcontrol.ReleaseAssetName)
	if err := os.WriteFile(srcWasm, []byte("new-wasm-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}

	if got := poundcontrol.FindSidecarWasm(srcDir); got != srcWasm {
		t.Fatalf("FindSidecarWasm = %q, want %q", got, srcWasm)
	}

	destPath, err := poundcontrol.InstallWasmFile(srcWasm, wasmDir, "v0.0.16")
	if err != nil {
		t.Fatalf("InstallWasmFile failed: %v", err)
	}
	wantDest := filepath.Join(wasmDir, poundcontrol.InstalledFileName)
	if destPath != wantDest {
		t.Fatalf("destPath = %q, want %q", destPath, wantDest)
	}

	gotBytes, err := os.ReadFile(destPath)
	if err != nil || string(gotBytes) != "new-wasm-bytes" {
		t.Fatalf("unexpected installed wasm content: %q (%v)", string(gotBytes), err)
	}
	if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
		t.Fatalf("expected legacy towncrier.wasm to be removed, stat err = %v", err)
	}

	// Verify .installed.json kept Twitch and wrote TownCrier.
	metaBytes, err := os.ReadFile(filepath.Join(wasmDir, ".installed.json"))
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Plugins map[string]map[string]any `json:"plugins"`
	}
	if err := json.Unmarshal(metaBytes, &parsed); err != nil {
		t.Fatal(err)
	}
	if _, ok := parsed.Plugins["com.github.mrpoundsign.poundcontrol.twitch"]; !ok {
		t.Fatal("expected pre-existing Twitch install record to be preserved")
	}
	tcRec, ok := parsed.Plugins[poundcontrol.PluginUUID]
	if !ok {
		t.Fatalf("expected TownCrier record %q in .installed.json", poundcontrol.PluginUUID)
	}
	if tcRec["installedVersion"] != "0.0.16" || tcRec["assetName"] != poundcontrol.ReleaseAssetName {
		t.Fatalf("unexpected TownCrier metadata record: %+v", tcRec)
	}

	st := poundcontrol.CheckPluginStatus(wasmDir, "v0.0.16")
	if !st.Installed || st.NeedsUpdate || st.InstalledVersion != "0.0.16" || !st.PoundControlDetected {
		t.Fatalf("unexpected up-to-date status: %+v", st)
	}

	stNewer := poundcontrol.CheckPluginStatus(wasmDir, "v0.0.17")
	if !stNewer.Installed || !stNewer.NeedsUpdate {
		t.Fatalf("expected NeedsUpdate=true for v0.0.17, got %+v", stNewer)
	}
}
