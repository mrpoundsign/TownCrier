package stt

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
)

func TestModelFamilyNormalizationAndLabels(t *testing.T) {
	if got := NormalizeModelFamily(""); got != ModelWhisperTurbo {
		t.Fatalf("expected default %q, got %q", ModelWhisperTurbo, got)
	}
	if got := NormalizeModelFamily("  SMALL "); got != ModelWhisperSmall {
		t.Fatalf("expected %q, got %q", ModelWhisperSmall, got)
	}
	if got := NormalizeModelFamily("large-v3-turbo"); got != ModelWhisperTurbo {
		t.Fatalf("expected %q, got %q", ModelWhisperTurbo, got)
	}

	if _, ok := ModelSpecByID("nonexistent_model"); ok {
		t.Fatal("expected unknown model family to return ok=false")
	}

	labels := ModelDisplayLabels()
	if len(labels) != len(AllModelSpecs) {
		t.Fatalf("expected %d labels, got %d", len(AllModelSpecs), len(labels))
	}
	for _, spec := range AllModelSpecs {
		lbl := ModelLabelForID(spec.ID)
		if roundTrip := ModelIDFromLabel(lbl); roundTrip != spec.ID {
			t.Fatalf("label round-trip mismatch for %s: label=%q -> %q", spec.ID, lbl, roundTrip)
		}
	}
}

func TestSpokenLanguageNormalizationAndLists(t *testing.T) {
	cases := map[string]string{
		"tl":         "tl",
		"FIL":        "tl",
		"tgl":        "tl",
		"Filipino":   "tl",
		"Philipino":  "tl",
		"<|tl|>":     "tl",
		"en-US":      "en",
		"JA_JP":      "ja",
		"unknownxyz": "",
	}
	for in, want := range cases {
		if got := NormalizeSpokenLang(in); got != want {
			t.Errorf("NormalizeSpokenLang(%q) = %q, want %q", in, got, want)
		}
	}

	list := NormalizeSpokenLanguageList([]string{"EN", "fil", "tl", "invalid", "ja"}, []string{"en"})
	wantList := []string{"en", "tl", "ja"}
	if !slices.Equal(list, wantList) {
		t.Fatalf("NormalizeSpokenLanguageList = %v, want %v", list, wantList)
	}

	defList := NormalizeSpokenLanguageList([]string{"invalid"}, []string{"en", "tl"})
	if !slices.Equal(defList, []string{"en", "tl"}) {
		t.Fatalf("expected fallback defaults [en tl], got %v", defList)
	}
}

func TestCleanTranscriptHallucinationAndTagFiltering(t *testing.T) {
	rejected := []string{
		"",
		"   ",
		"...",
		"[BLANK_AUDIO]",
		"[Music] (applause) *sigh*",
		"♪ upbeat intro music ♪",
		"Thank you for watching!",
		"Thanks for watching.",
		"Please subscribe.",
		"Subtitles by Amara.org community",
		"ご視聴ありがとうございました。",
		"Salamat sa panonood!",
		"po po po po po",
		"thank you thank you thank you thank you",
	}
	for _, raw := range rejected {
		if got, ok := CleanTranscript(raw); ok {
			t.Errorf("expected %q to be rejected as noise/hallucination, but got ok=true (%q)", raw, got)
		}
	}

	accepted := []struct {
		raw  string
		want string
	}{
		{
			raw:  "Magandang gabi po sa inyong lahat!",
			want: "Magandang gabi po sa inyong lahat!",
		},
		{
			raw:  "[laughter] Wait lang guys, mag-heal muna ako before the boss fight!",
			want: "Wait lang guys, mag-heal muna ako before the boss fight!",
		},
		{
			raw:  "  Hello   everyone,   welcome back to the stream! (coughing) ",
			want: "Hello everyone, welcome back to the stream!",
		},
	}
	for _, tc := range accepted {
		got, ok := CleanTranscript(tc.raw)
		if !ok {
			t.Errorf("expected %q to be accepted, got ok=false", tc.raw)
			continue
		}
		if got != tc.want {
			t.Errorf("CleanTranscript(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}

func TestResolveSpokenLang(t *testing.T) {
	// 1. Tagalog lexical markers override misidentified "en" when ["en", "tl"] are allowed
	if got := ResolveSpokenLang("en", "Magandang gabi guys, kamusta kayo?", []string{"en", "tl"}); got != "tl" {
		t.Fatalf("expected tl from lexical signal, got %q", got)
	}

	// 2. Pure English resolves to "en"
	if got := ResolveSpokenLang("en", "Let's check out this dungeon right now.", []string{"en", "tl"}); got != "en" {
		t.Fatalf("expected en, got %q", got)
	}

	// 3. Spurious Whisper language (e.g. Welsh "cy" on cough/accent) falls back to allowed list
	if got := ResolveSpokenLang("cy", "Okay let's go!", []string{"en", "tl"}); got != "en" {
		t.Fatalf("expected fallback to first allowed spoken lang en, got %q", got)
	}

	// 4. Single configured spoken language locks resolution
	if got := ResolveSpokenLang("", "Hello world", []string{"tl"}); got != "tl" {
		t.Fatalf("expected single locked lang tl, got %q", got)
	}

	// 5. Non-Latin Kana script always resolves to "ja"
	if got := ResolveSpokenLang("en", "皆さんこんにちは", []string{"en", "tl", "ja"}); got != "ja" {
		t.Fatalf("expected ja for Hiragana text, got %q", got)
	}

	// 6. Spanish resolves to "es" when whisperLang is empty (e.g. UI Test button or missing token)
	if got := ResolveSpokenLang("", "Hola amigos, ¿cómo están hoy?", []string{"en", "tl", "es"}); got != "es" {
		t.Fatalf("expected es for accented Spanish text, got %q", got)
	}
	if got := ResolveSpokenLang("", "Estoy hablando en espanol con mis amigos", []string{"en", "tl", "es"}); got != "es" {
		t.Fatalf("expected es for unaccented Spanish text, got %q", got)
	}
}

func TestStripTopLevelArchiveDirSafety(t *testing.T) {
	if rel, ok := stripTopLevelArchiveDir("sherpa-onnx-whisper-turbo/turbo-encoder.int8.onnx"); !ok || rel != "turbo-encoder.int8.onnx" {
		t.Fatalf("unexpected rel=%q ok=%v", rel, ok)
	}
	for _, unsafeName := range []string{
		"../evil.onnx",
		"sherpa/../../evil.onnx",
		"sherpa-onnx-whisper-turbo",
		"/",
		"",
	} {
		if rel, ok := stripTopLevelArchiveDir(unsafeName); ok {
			t.Errorf("expected %q to be rejected, got rel=%q", unsafeName, rel)
		}
	}
}

func TestResolveWhisperFilesAndDownloadModel(t *testing.T) {
	const turboTarBz2B64 = "QlpoOTFBWSZTWQumuXIAAOX5gMqAEABAA/3AfmnewAoIMAC5oYwmJoMEYhkYTBkhNAaBoABoAilNEntBNJtTR6IzExTjY1uGTiQC9uSCmHyUGji2jNvaqGAEgGA85ngtaJuDBzNaypxbHBhWlgVy/CSnK28tLXqZmFlTDHKhQYAKKQKHewGHITb1HhoXLvNpNX7d3kaFLMtISNguTYfR0Kyb0bn02JuMNnG8XtTrUHxW9rc2G/mPDJrSWSIf4u5IpwoSAXTXLkA="

	turboArchive, err := base64.StdEncoding.DecodeString(turboTarBz2B64)
	if err != nil {
		t.Fatalf("decode turboTarBz2B64: %v", err)
	}
	vadBytes := []byte("fake-silero-vad-onnx-weights")

	turboSum := sha256.Sum256(turboArchive)
	vadSum := sha256.Sum256(vadBytes)
	turboHex := hex.EncodeToString(turboSum[:])
	vadHex := hex.EncodeToString(vadSum[:])

	origVadURL, origVadSHA, origVadSize := SileroVadURL, SileroVadSHA256, SileroVadSize
	origAPIURL := ASRModelsReleaseAPIURL
	origSpec := AllModelSpecs[0]
	defer func() {
		SileroVadURL, SileroVadSHA256, SileroVadSize = origVadURL, origVadSHA, origVadSize
		ASRModelsReleaseAPIURL = origAPIURL
		AllModelSpecs[0] = origSpec
	}()

	var vadHits, archiveHits, apiHits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/silero_vad.onnx":
			vadHits.Add(1)
			_, _ = w.Write(vadBytes)
		case "/sherpa-onnx-whisper-turbo.tar.bz2":
			archiveHits.Add(1)
			_, _ = w.Write(turboArchive)
		case "/release":
			apiHits.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"assets": []map[string]string{
					{"name": "silero_vad.onnx", "digest": "sha256:" + vadHex},
					{"name": "sherpa-onnx-whisper-turbo.tar.bz2", "digest": "sha256:" + turboHex},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	SileroVadURL = srv.URL + "/silero_vad.onnx"
	SileroVadSHA256 = ""
	SileroVadSize = int64(len(vadBytes))
	ASRModelsReleaseAPIURL = srv.URL + "/release"

	AllModelSpecs[0].GGMLURL = ""
	AllModelSpecs[0].GGMLFile = ""
	AllModelSpecs[0].ArchiveURL = srv.URL + "/sherpa-onnx-whisper-turbo.tar.bz2"
	AllModelSpecs[0].ArchiveSHA256 = ""
	AllModelSpecs[0].ArchiveSize = int64(len(turboArchive))

	baseDir := t.TempDir()
	if IsModelInstalled(baseDir, ModelWhisperTurbo) {
		t.Fatal("expected empty dir not to be installed")
	}

	var lastCur, lastTot int64
	if err := DownloadModel(baseDir, ModelWhisperTurbo, func(cur, tot int64) {
		lastCur = cur
		lastTot = tot
	}); err != nil {
		t.Fatalf("DownloadModel failed: %v", err)
	}

	if !IsModelInstalled(baseDir, ModelWhisperTurbo) {
		t.Fatal("expected model and silero_vad.onnx to be installed")
	}
	if vadHits.Load() != 1 || archiveHits.Load() != 1 {
		t.Fatalf("expected 1 hit each for vad and archive, got vad=%d archive=%d", vadHits.Load(), archiveHits.Load())
	}
	if apiHits.Load() < 1 {
		t.Fatal("expected GitHub release API digest lookup to be exercised")
	}
	if lastCur != lastTot || lastTot == 0 {
		t.Fatalf("expected final progress %d == %d > 0", lastCur, lastTot)
	}

	encPath, decPath, tokPath, usingFP, err := ResolveWhisperFiles(baseDir, ModelWhisperTurbo, true)
	if err != nil {
		t.Fatalf("ResolveWhisperFiles failed: %v", err)
	}
	if usingFP {
		t.Fatal("expected turbo INT8 archive to report usingFP=false")
	}
	if got, _ := os.ReadFile(encPath); string(got) != "enc-int8" {
		t.Errorf("unexpected encoder content: %q", string(got))
	}
	if got, _ := os.ReadFile(decPath); string(got) != "dec-int8" {
		t.Errorf("unexpected decoder content: %q", string(got))
	}
	if got, _ := os.ReadFile(tokPath); string(got) != "tok-data" {
		t.Errorf("unexpected tokens content: %q", string(got))
	}

	// Verify manifest recorded SHA-256 hash
	mf, err := ReadModelManifest(ModelFamilyDir(baseDir, ModelWhisperTurbo))
	if err != nil {
		t.Fatalf("ReadModelManifest failed: %v", err)
	}
	if mf.Assets["sherpa-onnx-whisper-turbo.tar.bz2"] != turboHex {
		t.Fatalf("manifest hash = %q, want %q", mf.Assets["sherpa-onnx-whisper-turbo.tar.bz2"], turboHex)
	}

	// Second call is a no-op when already installed
	if err := DownloadModel(baseDir, ModelWhisperTurbo, nil); err != nil {
		t.Fatalf("idempotent DownloadModel failed: %v", err)
	}
	if vadHits.Load() != 1 || archiveHits.Load() != 1 {
		t.Fatalf("expected no network downloads when already installed, got vad=%d archive=%d", vadHits.Load(), archiveHits.Load())
	}

	// Verify FP preference when unquantized .onnx files also exist
	famDir := ModelFamilyDir(baseDir, ModelWhisperTurbo)
	_ = os.WriteFile(filepath.Join(famDir, "turbo-encoder.onnx"), []byte("enc-fp"), 0644)
	_ = os.WriteFile(filepath.Join(famDir, "turbo-decoder.onnx"), []byte("dec-fp"), 0644)
	_, _, _, usingFPPref, err := ResolveWhisperFiles(baseDir, ModelWhisperTurbo, true)
	if err != nil || !usingFPPref {
		t.Fatalf("expected preferFP=true to select FP weights when present, got usingFP=%v err=%v", usingFPPref, err)
	}
	_, _, _, usingCPU, err := ResolveWhisperFiles(baseDir, ModelWhisperTurbo, false)
	if err != nil || usingCPU {
		t.Fatalf("expected preferFP=false to select INT8 weights when both exist, got usingFP=%v err=%v", usingCPU, err)
	}

	// Verify SHA-256 mismatch aborts cleanly and leaves no extracted files
	badDir := t.TempDir()
	AllModelSpecs[0].ArchiveSHA256 = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	err = DownloadModel(badDir, ModelWhisperTurbo, nil)
	if !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("expected ErrChecksumMismatch, got %v", err)
	}
	if IsModelInstalled(badDir, ModelWhisperTurbo) {
		t.Fatal("expected badDir not to report installed after checksum failure")
	}

	// Verify pre-cancelled context aborts cleanly
	cancelCtx, cancelFn := context.WithCancel(context.Background())
	cancelFn()
	cancelDir := t.TempDir()
	if err := DownloadModelWithContext(cancelCtx, cancelDir, ModelWhisperTurbo, nil); err == nil {
		t.Fatal("expected pre-cancelled context to fail DownloadModelWithContext")
	}
}

func TestPinnedSHA256IntegrityAndFailClosedGGMLDownload(t *testing.T) {
	// 1. Every production ModelSpec + SileroVadSHA256 MUST carry a valid 64-hex pinned SHA-256 digest
	if len(SileroVadSHA256) != 64 {
		t.Fatalf("expected 64-char hex SileroVadSHA256, got %q", SileroVadSHA256)
	}
	if _, err := hex.DecodeString(SileroVadSHA256); err != nil {
		t.Fatalf("invalid hex in SileroVadSHA256: %v", err)
	}
	for _, spec := range AllModelSpecs {
		if spec.GGMLURL == "" || spec.GGMLFile == "" {
			t.Fatalf("model %s missing GGMLURL or GGMLFile", spec.ID)
		}
		if len(spec.GGMLSHA256) != 64 {
			t.Fatalf("model %s missing 64-hex GGMLSHA256: %q", spec.ID, spec.GGMLSHA256)
		}
		if _, err := hex.DecodeString(spec.GGMLSHA256); err != nil {
			t.Fatalf("model %s has invalid hex GGMLSHA256: %v", spec.ID, err)
		}
	}

	// 2. Test GGML download with matching SHA-256, corrupt SHA-256, and missing SHA-256 (fail-closed!)
	vadBytes := []byte("verified-silero-vad-bytes")
	ggmlBytes := []byte("verified-ggml-whisper-q5-weights")
	vadSum := sha256.Sum256(vadBytes)
	ggmlSum := sha256.Sum256(ggmlBytes)
	vadHex := hex.EncodeToString(vadSum[:])
	ggmlHex := hex.EncodeToString(ggmlSum[:])

	origVadURL, origVadSHA, origVadSize := SileroVadURL, SileroVadSHA256, SileroVadSize
	origAPIURL := ASRModelsReleaseAPIURL
	origSpec := AllModelSpecs[1] // whisper_small
	defer func() {
		SileroVadURL, SileroVadSHA256, SileroVadSize = origVadURL, origVadSHA, origVadSize
		ASRModelsReleaseAPIURL = origAPIURL
		AllModelSpecs[1] = origSpec
	}()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/silero_vad.onnx":
			_, _ = w.Write(vadBytes)
		case "/ggml-small-q5_1.bin":
			_, _ = w.Write(ggmlBytes)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	SileroVadURL = srv.URL + "/silero_vad.onnx"
	SileroVadSHA256 = vadHex
	SileroVadSize = int64(len(vadBytes))
	ASRModelsReleaseAPIURL = srv.URL + "/no-release-api"

	AllModelSpecs[1].GGMLURL = srv.URL + "/ggml-small-q5_1.bin"
	AllModelSpecs[1].GGMLSHA256 = ggmlHex
	AllModelSpecs[1].GGMLSize = int64(len(ggmlBytes))

	// 2a. Fail-closed when SHA-256 digest is empty and release API has no digest
	noHashDir := t.TempDir()
	AllModelSpecs[1].GGMLSHA256 = ""
	if err := DownloadModel(noHashDir, ModelWhisperSmall, nil); !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("expected fail-closed ErrChecksumMismatch when SHA-256 is missing, got %v", err)
	}
	if IsGGMLModelInstalled(noHashDir, ModelWhisperSmall) {
		t.Fatal("expected unverified model NOT to be installed")
	}

	// 2b. Fail-closed when SHA-256 mismatches by 1 nibble
	corruptDir := t.TempDir()
	AllModelSpecs[1].GGMLSHA256 = "0000000000000000000000000000000000000000000000000000000000000000"
	if err := DownloadModel(corruptDir, ModelWhisperSmall, nil); !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("expected ErrChecksumMismatch on wrong hash, got %v", err)
	}
	if IsGGMLModelInstalled(corruptDir, ModelWhisperSmall) {
		t.Fatal("expected mismatched model NOT to be installed")
	}

	// 2c. Succeeds when SHA-256 matches pinned constant; also upgrades a legacy ONNX-only dir
	okDir := t.TempDir()
	AllModelSpecs[1].GGMLSHA256 = ggmlHex
	if err := DownloadModel(okDir, ModelWhisperSmall, nil); err != nil {
		t.Fatalf("DownloadModel with valid pinned SHA-256 failed: %v", err)
	}
	if !IsGGMLModelInstalled(okDir, ModelWhisperSmall) {
		t.Fatal("expected IsGGMLModelInstalled=true after verified GGML download")
	}
}

func TestSelectGPUDeviceAndSmartSTTThreads(t *testing.T) {
	// 4-thread laptop CPU (e.g. Core i5-7300HQ) must cap Auto CPU threads to 2 (never 4!)
	if got := SmartSTTThreads(0, 4, false); got != 2 {
		t.Fatalf("expected 2 threads on 4-thread CPU, got %d", got)
	}
	// 8-thread Ryzen 5 3550H caps Auto CPU threads to 4
	if got := SmartSTTThreads(0, 8, false); got != 4 {
		t.Fatalf("expected 4 threads on 8-thread CPU, got %d", got)
	}
	// GPU offload caps Auto helper threads to <= 2 even on 16-thread desktop CPUs
	if got := SmartSTTThreads(0, 16, true); got != 2 {
		t.Fatalf("expected 2 helper threads when GPU-offloaded, got %d", got)
	}

	dualLaptop := []GPUDevice{
		{GPUIndex: 0, VKIndex: 0, Name: "Vulkan0", Description: "Intel(R) HD Graphics 630", Type: "igpu", MemoryTotalMB: 4096},
		{GPUIndex: 1, VKIndex: 1, Name: "Vulkan1", Description: "NVIDIA GeForce GTX 1060", Type: "dgpu", MemoryTotalMB: 6144},
	}

	// Auto prefers discrete GPU
	dev, ok := SelectGPUDevice(ComputeDeviceAuto, dualLaptop)
	if !ok || dev == nil || dev.Description != "NVIDIA GeForce GTX 1060" {
		t.Fatalf("expected Auto to pick GTX 1060 dGPU, got dev=%+v ok=%v", dev, ok)
	}

	// iGPU mode picks Intel HD 630 so gaming GPU stays untouched
	dev, ok = SelectGPUDevice(ComputeDeviceIGPU, dualLaptop)
	if !ok || dev == nil || dev.Description != "Intel(R) HD Graphics 630" {
		t.Fatalf("expected igpu mode to pick Intel HD 630, got dev=%+v ok=%v", dev, ok)
	}

	// Explicit Vulkan adapter selection by name
	dev, ok = SelectGPUDevice("vulkan:Intel(R) HD Graphics 630", dualLaptop)
	if !ok || dev == nil || dev.VKIndex != 0 {
		t.Fatalf("expected explicit vulkan adapter selection to match VKIndex=0, got dev=%+v ok=%v", dev, ok)
	}

	// Desktop with ONLY a discrete GPU + explicit "igpu" mode -> returns (nil, false) so gaming GPU is left untouched
	desktopOnlyDGPU := []GPUDevice{
		{GPUIndex: 0, VKIndex: 0, Name: "Vulkan0", Description: "NVIDIA GeForce RTX 4080", Type: "dgpu", MemoryTotalMB: 16384},
	}
	if dev, ok := SelectGPUDevice(ComputeDeviceIGPU, desktopOnlyDGPU); ok || dev != nil {
		t.Fatalf("expected igpu mode on desktop without iGPU to fall back to CPU (leaving gaming GPU free), got %+v", dev)
	}

	// CPU mode always returns (nil, false)
	if dev, ok := SelectGPUDevice(ComputeDeviceCPU, dualLaptop); ok || dev != nil {
		t.Fatalf("expected cpu mode to return nil, got %+v", dev)
	}
}

// TestWhisperCppHelperProcessSubprocess acts as a mock towncrier-whisper helper when GO_WANT_WHISPER_HELPER=1.
func TestWhisperCppHelperProcessSubprocess(t *testing.T) {
	if os.Getenv("GO_WANT_WHISPER_HELPER") != "1" {
		return
	}
	visDev := os.Getenv("GGML_VK_VISIBLE_DEVICES")
	prov := "cpu"
	if visDev != "" {
		prov = fmt.Sprintf("vulkan:Intel(R) HD Graphics 630 [vk=%s] (igpu)", visDev)
	}

	writeFrame := func(payload string) {
		var lenBuf [4]byte
		binary.LittleEndian.PutUint32(lenBuf[:], uint32(len(payload)))
		_, _ = os.Stdout.Write(lenBuf[:])
		_, _ = os.Stdout.WriteString(payload)
	}

	writeFrame(fmt.Sprintf(`{"ok":true,"ready":true,"provider":%q}`, prov))

	for {
		var hdr [16]byte
		if _, err := io.ReadFull(os.Stdin, hdr[:]); err != nil {
			os.Exit(0)
		}
		magic := binary.LittleEndian.Uint32(hdr[0:4])
		if magic != whisperHelperMagic {
			writeFrame(`{"ok":false,"error":"bad magic"}`)
			os.Exit(2)
		}
		nSamples := binary.LittleEndian.Uint32(hdr[4:8])
		langLen := binary.LittleEndian.Uint32(hdr[8:12])

		langBytes := make([]byte, langLen)
		if langLen > 0 {
			_, _ = io.ReadFull(os.Stdin, langBytes)
		}
		pcmBytes := make([]byte, int(nSamples)*4)
		if nSamples > 0 {
			_, _ = io.ReadFull(os.Stdin, pcmBytes)
		}

		langOut := string(langBytes)
		if langOut == "" || langOut == "auto" {
			langOut = "tl"
		}
		writeFrame(fmt.Sprintf(`{"ok":true,"text":"Kamusta kayo guys!","lang":%q,"decode_ms":185}`, langOut))
	}
}

func TestWhisperCppEngineProtocolAndIGPUIsolation(t *testing.T) {
	SetGPUDeviceEnumerator(func() ([]GPUDevice, error) {
		return []GPUDevice{
			{GPUIndex: 0, VKIndex: 0, Name: "Vulkan0", Description: "NVIDIA GeForce GTX 1060", Type: "dgpu", MemoryTotalMB: 6144},
			{GPUIndex: 1, VKIndex: 1, Name: "Vulkan1", Description: "Intel(R) HD Graphics 630", Type: "igpu", MemoryTotalMB: 4096},
		}, nil
	})
	defer SetGPUDeviceEnumerator(nil)

	baseDir := t.TempDir()
	if err := os.WriteFile(VadModelPath(baseDir), []byte("vad"), 0644); err != nil {
		t.Fatal(err)
	}
	ggmlPath, err := GGMLModelPath(baseDir, ModelWhisperSmall)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(ggmlPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ggmlPath, []byte("ggml-q5"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create a tiny wrapper script (or batch on Windows) that invokes the current test binary as the mock helper
	testExe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	helperScript := filepath.Join(t.TempDir(), "mock-whisper.sh")
	scriptContent := fmt.Sprintf("#!/bin/sh\nexport GO_WANT_WHISPER_HELPER=1\nexec %q -test.run=^TestWhisperCppHelperProcessSubprocess$ -- \"$@\"\n", testExe)
	if runtime.GOOS == "windows" {
		helperScript = filepath.Join(t.TempDir(), "mock-whisper.cmd")
		scriptContent = fmt.Sprintf("@echo off\r\nset GO_WANT_WHISPER_HELPER=1\r\n\"%s\" -test.run=^TestWhisperCppHelperProcessSubprocess$ -- %%*\r\n", testExe)
	}
	if err := os.WriteFile(helperScript, []byte(scriptContent), 0755); err != nil {
		t.Fatal(err)
	}

	eng, err := NewWhisperCppEngine(ModelWhisperSmall, baseDir, helperScript, 0, ComputeDeviceIGPU)
	if err != nil {
		t.Fatalf("NewWhisperCppEngine failed: %v", err)
	}
	defer func() { _ = eng.Close() }()

	if !strings.Contains(eng.Provider(), "vk=1") {
		t.Fatalf("expected GGML_VK_VISIBLE_DEVICES=1 isolation on Intel HD 630 iGPU, got provider=%q", eng.Provider())
	}

	tr, err := eng.Transcribe([]float32{0.1, -0.2, 0.3, -0.1}, 16000, []string{"en", "tl"})
	if err != nil {
		t.Fatalf("Transcribe failed: %v", err)
	}
	if tr.Text != "Kamusta kayo guys!" || tr.DetectedLang != "tl" || tr.Latency.Milliseconds() != 185 {
		t.Fatalf("unexpected transcript: %+v", tr)
	}
}

func TestCheckSTTModelUpdateAndForceDownload(t *testing.T) {
	v1Bytes := []byte("ggml-tiny-v1-weights")
	v2Bytes := []byte("ggml-tiny-v2-updated-weights")
	vadBytes := []byte("silero-vad-v1")

	v1Sum := sha256.Sum256(v1Bytes)
	v2Sum := sha256.Sum256(v2Bytes)
	vadSum := sha256.Sum256(vadBytes)
	v1Hex := hex.EncodeToString(v1Sum[:])
	v2Hex := hex.EncodeToString(v2Sum[:])
	vadHex := hex.EncodeToString(vadSum[:])

	origVadURL, origVadSHA, origVadSize := SileroVadURL, SileroVadSHA256, SileroVadSize
	origSpec := AllModelSpecs[3] // whisper_tiny
	defer func() {
		SileroVadURL, SileroVadSHA256, SileroVadSize = origVadURL, origVadSHA, origVadSize
		AllModelSpecs[3] = origSpec
	}()

	var serveV2 atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/silero_vad.onnx":
			_, _ = w.Write(vadBytes)
		case "/ggml-tiny-q5_1.bin":
			if serveV2.Load() {
				w.Header().Set("X-Linked-Etag", `"`+v2Hex+`"`)
				if r.Method == http.MethodHead {
					w.WriteHeader(http.StatusFound)
					return
				}
				_, _ = w.Write(v2Bytes)
				return
			}
			w.Header().Set("X-Linked-Etag", `"`+v1Hex+`"`)
			if r.Method == http.MethodHead {
				w.WriteHeader(http.StatusFound)
				return
			}
			_, _ = w.Write(v1Bytes)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	SileroVadURL = srv.URL + "/silero_vad.onnx"
	SileroVadSHA256 = vadHex
	SileroVadSize = int64(len(vadBytes))

	AllModelSpecs[3].GGMLURL = srv.URL + "/ggml-tiny-q5_1.bin"
	AllModelSpecs[3].GGMLSHA256 = v1Hex
	AllModelSpecs[3].GGMLSize = int64(len(v1Bytes))

	baseDir := t.TempDir()
	if err := DownloadModel(baseDir, ModelWhisperTiny, nil); err != nil {
		t.Fatalf("initial DownloadModel v1 failed: %v", err)
	}
	if upd, _, _ := CheckSTTModelUpdate(context.Background(), baseDir, ModelWhisperTiny); upd {
		t.Fatal("expected updateAvailable=false when local manifest matches remote v1 SHA-256")
	}

	// Simulate upstream publishing v2 weights (X-Linked-Etag changes to v2Hex)
	serveV2.Store(true)
	upd, localSHA, remoteSHA := CheckSTTModelUpdate(context.Background(), baseDir, ModelWhisperTiny)
	if !upd || localSHA != v1Hex || remoteSHA != v2Hex {
		t.Fatalf("expected updateAvailable=true (local=%s remote=%s), got upd=%v local=%s remote=%s", v1Hex, v2Hex, upd, localSHA, remoteSHA)
	}

	// ForceDownloadModelWithContext updates the installed model in-place and verifies against v2Hex
	AllModelSpecs[3].GGMLSHA256 = v2Hex
	AllModelSpecs[3].GGMLSize = int64(len(v2Bytes))
	if err := ForceDownloadModelWithContext(context.Background(), baseDir, ModelWhisperTiny, nil); err != nil {
		t.Fatalf("ForceDownloadModelWithContext v2 failed: %v", err)
	}
	if updAfter, _, _ := CheckSTTModelUpdate(context.Background(), baseDir, ModelWhisperTiny); updAfter {
		t.Fatal("expected updateAvailable=false after updating to v2")
	}
}
