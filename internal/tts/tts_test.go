package tts

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mrpoundsign/TownCrier/internal/config"
)

func TestResolveSpeakerIDForModel(t *testing.T) {
	if id := ResolveSpeakerIDForModel("af", 11); id != 0 {
		t.Errorf("expected 0 for af, got %d", id)
	}
	if id := ResolveSpeakerIDForModel("af_bella", 11); id != 1 {
		t.Errorf("expected 1 for af_bella, got %d", id)
	}
	if id := ResolveSpeakerIDForModel("af_nicole", 11); id != 2 {
		t.Errorf("expected 2 for af_nicole, got %d", id)
	}
	if id := ResolveSpeakerIDForModel("  AF_SARAH  ", 11); id != 3 {
		t.Errorf("expected 3 for uppercase spaced AF_SARAH, got %d", id)
	}
	if id := ResolveSpeakerIDForModel("af_sky", 11); id != 4 {
		t.Errorf("expected 4 for af_sky, got %d", id)
	}
	if id := ResolveSpeakerIDForModel("am_adam", 11); id != 5 {
		t.Errorf("expected 5 for am_adam, got %d", id)
	}
	if id := ResolveSpeakerIDForModel("am_michael", 11); id != 6 {
		t.Errorf("expected 6 for am_michael, got %d", id)
	}
	if id := ResolveSpeakerIDForModel("bf_emma", 11); id != 7 {
		t.Errorf("expected 7 for bf_emma, got %d", id)
	}
	if id := ResolveSpeakerIDForModel("bf_isabella", 11); id != 8 {
		t.Errorf("expected 8 for bf_isabella, got %d", id)
	}
	if id := ResolveSpeakerIDForModel("bm_george", 11); id != 9 {
		t.Errorf("expected 9 for bm_george, got %d", id)
	}
	if id := ResolveSpeakerIDForModel("bm_lewis", 11); id != 10 {
		t.Errorf("expected 10 for bm_lewis, got %d", id)
	}
	if id := ResolveSpeakerIDForModel("Bella — US Female (af_bella)", 11); id != 1 {
		t.Errorf("expected 1 for display label Bella, got %d", id)
	}
	if id := ResolveSpeakerIDForModel("af_bella", 53); id != 2 {
		t.Errorf("expected 2 for af_bella in 53-speaker v1.0 model, got %d", id)
	}
	if id := ResolveSpeakerIDForModel("Adam — US Male (am_adam)", 53); id != 11 {
		t.Errorf("expected 11 for am_adam in 53-speaker v1.0 model, got %d", id)
	}
	if got := VoiceIDFromLabel("Emma — UK Female (bf_emma)"); got != "bf_emma" {
		t.Errorf("expected bf_emma from label, got %q", got)
	}
	if got := VoiceDisplayName("am_michael"); got != "Michael — US Male (am_michael)" {
		t.Errorf("unexpected display name: %q", got)
	}
	if id := ResolveSpeakerIDForModel("non_existent_voice", 11); id != 0 {
		t.Errorf("expected fallback 0 for unknown voice, got %d", id)
	}
}

func TestIsModelInstalled(t *testing.T) {
	tmpDir := t.TempDir()
	if IsModelInstalled(tmpDir) {
		t.Error("empty dir should not be reported as installed")
	}

	// Create only int8 + shared assets -> still missing model.onnx
	_ = os.WriteFile(filepath.Join(tmpDir, "model.int8.onnx"), []byte("x"), 0600)
	_ = os.WriteFile(filepath.Join(tmpDir, "voices.bin"), []byte("x"), 0600)
	_ = os.WriteFile(filepath.Join(tmpDir, "tokens.txt"), []byte("x"), 0600)
	_ = os.Mkdir(filepath.Join(tmpDir, "espeak-ng-data"), 0755)

	if IsModelInstalled(tmpDir) {
		t.Error("dir without model.onnx should not be reported as installed")
	}

	_ = os.WriteFile(filepath.Join(tmpDir, "model.onnx"), []byte("x"), 0600)
	if !IsModelInstalled(tmpDir) {
		t.Error("dir with all assets (both model.onnx and model.int8.onnx) should be reported as installed")
	}
}

func TestDownloadKokoroModelParallel(t *testing.T) {
	const fp32B64 = "QlpoOTFBWSZTWYefy4YAAVl/gsKgACBAA/+wIQBAAP/v38BAADAIMAE4FBpQ0AGgAAAAAGMhkNBoNGgDQANDBFEmk0aBR+SBM0aGjTao2mp1+R53/M/dNZgQWjmDLeSCRoQFtxxupukGAksHZFIndUcwOAwIEjCyOQyOk28TIfoxHZbzj9eUQmkPHAHYCG/0eHD9iFQe3XQaC03f4Yv8qwREFRi0YR9hp4ZtY1sGh4xSgSwM2DnEsXaWUsPYGo2jJEToDUNaA0NEjI3D2SPgnIgeSccg+E34dMTpdaPHgZEmY4ZwaC4PxeDDb1j5BAwJVqIFyCbMRPBREH2Be4CYRD3wNQenMxG8c4N198RB1Hv6Rcmcd2PXb1HAa1nTq7BA/4u5IpwoSEPP5cMA"
	const int8B64 = "QlpoOTFBWSZTWZDFmCAAAWN/gsKgACBAA//kIAgAAP7v38BAADAIMAE4a0GkgAGgAAABoAaIygRiYJoaMRk0MTARSKYQ1HqZpqMjTTR6jDUNPUy5D1/bOc5d5cQUjUGPAkCRoQFpv2VznIGAkXjbBE76qRhS4YECQwshxGQ2FvyeY4j0Ngzp6ylr4RCWBbrBtBCOJx941CFAMDAP4OzfOHOMigYVwHdhYNdQwRhASlIJpEwEDY0ARBGoFAJ9wEgJGIDVFAdBKaoYHMUbQJjNNA3yHMnQgeifWIck36u2B2rpHS8ICQOG8XAwDoaBOnvC449V+MZQEy9IC6BHUInAtImB4dB/vg3HiOuBoB5lw6RrBq06IiDIdvaMwFaZx2XZW5DcNFEq+68QP8XckU4UJCQxZggA"

	fp32Archive, err := base64.StdEncoding.DecodeString(fp32B64)
	if err != nil {
		t.Fatalf("decode fp32B64: %v", err)
	}
	int8Archive, err := base64.StdEncoding.DecodeString(int8B64)
	if err != nil {
		t.Fatalf("decode int8B64: %v", err)
	}

	fp32Sum := sha256.Sum256(fp32Archive)
	int8Sum := sha256.Sum256(int8Archive)

	origFP32URL, origFP32SHA, origFP32Size := KokoroFP32ArchiveURL, KokoroFP32ArchiveSHA256, KokoroFP32ArchiveSize
	origINT8URL, origINT8SHA, origINT8Size := KokoroINT8ArchiveURL, KokoroINT8ArchiveSHA256, KokoroINT8ArchiveSize
	defer func() {
		KokoroFP32ArchiveURL, KokoroFP32ArchiveSHA256, KokoroFP32ArchiveSize = origFP32URL, origFP32SHA, origFP32Size
		KokoroINT8ArchiveURL, KokoroINT8ArchiveSHA256, KokoroINT8ArchiveSize = origINT8URL, origINT8SHA, origINT8Size
	}()

	var fp32Hits, int8Hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/fp32.tar.bz2":
			fp32Hits.Add(1)
			_, _ = w.Write(fp32Archive)
		case "/int8.tar.bz2":
			int8Hits.Add(1)
			_, _ = w.Write(int8Archive)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	KokoroFP32ArchiveURL = server.URL + "/fp32.tar.bz2"
	KokoroFP32ArchiveSHA256 = hex.EncodeToString(fp32Sum[:])
	KokoroFP32ArchiveSize = int64(len(fp32Archive))

	KokoroINT8ArchiveURL = server.URL + "/int8.tar.bz2"
	KokoroINT8ArchiveSHA256 = hex.EncodeToString(int8Sum[:])
	KokoroINT8ArchiveSize = int64(len(int8Archive))

	destDir := t.TempDir()
	var lastCur, lastTot int64
	err = DownloadKokoroModelForMode(destDir, true, func(cur, tot int64) {
		lastCur = cur
		lastTot = tot
	})
	if err != nil {
		t.Fatalf("DownloadKokoroModelForMode failed: %v", err)
	}

	if !IsModelInstalled(destDir) {
		t.Fatal("expected model to be installed after parallel download")
	}
	if fp32Hits.Load() != 1 || int8Hits.Load() != 1 {
		t.Errorf("expected 1 hit each for fp32 and int8, got fp32=%d int8=%d", fp32Hits.Load(), int8Hits.Load())
	}
	if lastCur != lastTot || lastTot == 0 {
		t.Errorf("expected final progress %d == %d > 0", lastCur, lastTot)
	}

	// Verify file contents
	if got, _ := os.ReadFile(filepath.Join(destDir, "model.onnx")); string(got) != "fp32-weights" {
		t.Errorf("unexpected model.onnx content: %q", string(got))
	}
	if got, _ := os.ReadFile(filepath.Join(destDir, "model.int8.onnx")); string(got) != "int8-weights" {
		t.Errorf("unexpected model.int8.onnx content: %q", string(got))
	}
	if got, _ := os.ReadFile(filepath.Join(destDir, "espeak-ng-data", "en_dict")); string(got) != "dict-data" {
		t.Errorf("unexpected espeak-ng-data/en_dict content: %q", string(got))
	}

	// Verify checksum failure cleans up staged files and returns ErrChecksumMismatch
	badDir := t.TempDir()
	KokoroINT8ArchiveSHA256 = "0000000000000000000000000000000000000000000000000000000000000000"
	err = DownloadKokoroModelForMode(badDir, true, nil)
	if !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("expected ErrChecksumMismatch, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(badDir, "model.int8.onnx")); !os.IsNotExist(statErr) {
		t.Error("expected model.int8.onnx not to exist after checksum mismatch")
	}

	// Verify context cancellation aborts download cleanly
	cancelCtx, cancelFn := context.WithCancel(context.Background())
	cancelFn() // cancel immediately
	cancelDir := t.TempDir()
	err = DownloadKokoroModelForModeWithContext(cancelCtx, cancelDir, false, nil)
	if err == nil {
		t.Fatal("expected error on cancelled context, got nil")
	}
	if !errors.Is(err, context.Canceled) && !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("expected context cancellation error, got %v", err)
	}
}

func TestSherpaEngineSynthesis(t *testing.T) {
	modelDir := DefaultModelDir()
	if !IsModelInstalled(modelDir) {
		for _, candidate := range []string{"models/kokoro-en", "../../models/kokoro-en"} {
			if IsModelInstalled(candidate) {
				modelDir = candidate
				break
			}
		}
	}
	if !IsModelInstalled(modelDir) {
		t.Skip("local Kokoro model not installed, skipping live synthesis test")
	}

	engine, err := NewFamilySherpaEngine(ModelKokoro, modelDir, 2, false)
	if err != nil {
		t.Fatalf("NewFamilySherpaEngine failed: %v", err)
	}
	defer func() { _ = engine.Close() }()

	if len(engine.Voices()) == 0 {
		t.Error("expected non-empty voices list")
	}

	samples, sr, err := engine.Synthesize("Testing audio output.", "af_bella", 1.0)
	if err != nil {
		t.Fatalf("Synthesize failed: %v", err)
	}
	if len(samples) == 0 {
		t.Error("expected non-empty samples")
	}
	if sr != 24000 {
		t.Errorf("expected 24000 Hz, got %d", sr)
	}
}

func TestSplitIntoSentences(t *testing.T) {
	if got := SplitIntoSentences("   "); len(got) != 0 {
		t.Errorf("expected nil/empty for blank text, got %v", got)
	}

	short := "Hello streamer welcome to the stream"
	if got := SplitIntoSentences(short); len(got) != 1 || got[0] != short {
		t.Errorf("expected single sentence %q, got %v", short, got)
	}

	previewText := "Hello! I am reading your livestream chat with natural neural speech."
	got := SplitIntoSentences(previewText)
	if len(got) != 2 || got[0] != "Hello!" || got[1] != "I am reading your livestream chat with natural neural speech." {
		t.Errorf("expected [\"Hello!\", \"I am reading your livestream chat with natural neural speech.\"], got %q", got)
	}

	abbr := "Dr. Smith and Mr. Jones are here! Welcome to St. Louis."
	abbrGot := SplitIntoSentences(abbr)
	if len(abbrGot) != 2 || abbrGot[0] != "Dr. Smith and Mr. Jones are here!" || abbrGot[1] != "Welcome to St. Louis." {
		t.Errorf("unexpected sentence split with abbreviations: %q", abbrGot)
	}
}

func TestDownloadKokoroModelUnified(t *testing.T) {
	const fp32B64 = "QlpoOTFBWSZTWYefy4YAAVl/gsKgACBAA/+wIQBAAP/v38BAADAIMAE4FBpQ0AGgAAAAAGMhkNBoNGgDQANDBFEmk0aBR+SBM0aGjTao2mp1+R53/M/dNZgQWjmDLeSCRoQFtxxupukGAksHZFIndUcwOAwIEjCyOQyOk28TIfoxHZbzj9eUQmkPHAHYCG/0eHD9iFQe3XQaC03f4Yv8qwREFRi0YR9hp4ZtY1sGh4xSgSwM2DnEsXaWUsPYGo2jJEToDUNaA0NEjI3D2SPgnIgeSccg+E34dMTpdaPHgZEmY4ZwaC4PxeDDb1j5BAwJVqIFyCbMRPBREH2Be4CYRD3wNQenMxG8c4N198RB1Hv6Rcmcd2PXb1HAa1nTq7BA/4u5IpwoSEPP5cMA"
	const int8B64 = "QlpoOTFBWSZTWZDFmCAAAWN/gsKgACBAA//kIAgAAP7v38BAADAIMAE4a0GkgAGgAAABoAaIygRiYJoaMRk0MTARSKYQ1HqZpqMjTTR6jDUNPUy5D1/bOc5d5cQUjUGPAkCRoQFpv2VznIGAkXjbBE76qRhS4YECQwshxGQ2FvyeY4j0Ngzp6ylr4RCWBbrBtBCOJx941CFAMDAP4OzfOHOMigYVwHdhYNdQwRhASlIJpEwEDY0ARBGoFAJ9wEgJGIDVFAdBKaoYHMUbQJjNNA3yHMnQgeifWIck36u2B2rpHS8ICQOG8XAwDoaBOnvC449V+MZQEy9IC6BHUInAtImB4dB/vg3HiOuBoB5lw6RrBq06IiDIdvaMwFaZx2XZW5DcNFEq+68QP8XckU4UJCQxZggA"

	fp32Archive, err := base64.StdEncoding.DecodeString(fp32B64)
	if err != nil {
		t.Fatalf("decode fp32B64: %v", err)
	}
	int8Archive, err := base64.StdEncoding.DecodeString(int8B64)
	if err != nil {
		t.Fatalf("decode int8B64: %v", err)
	}
	fp32Sum := sha256.Sum256(fp32Archive)
	int8Sum := sha256.Sum256(int8Archive)

	origFP32URL, origFP32SHA, origFP32Size := KokoroFP32ArchiveURL, KokoroFP32ArchiveSHA256, KokoroFP32ArchiveSize
	origINT8URL, origINT8SHA, origINT8Size := KokoroINT8ArchiveURL, KokoroINT8ArchiveSHA256, KokoroINT8ArchiveSize
	defer func() {
		KokoroFP32ArchiveURL, KokoroFP32ArchiveSHA256, KokoroFP32ArchiveSize = origFP32URL, origFP32SHA, origFP32Size
		KokoroINT8ArchiveURL, KokoroINT8ArchiveSHA256, KokoroINT8ArchiveSize = origINT8URL, origINT8SHA, origINT8Size
	}()

	var fp32Hits, int8Hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/fp32.tar.bz2":
			fp32Hits.Add(1)
			_, _ = w.Write(fp32Archive)
		case "/int8.tar.bz2":
			int8Hits.Add(1)
			_, _ = w.Write(int8Archive)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	KokoroFP32ArchiveURL = server.URL + "/fp32.tar.bz2"
	KokoroFP32ArchiveSHA256 = hex.EncodeToString(fp32Sum[:])
	KokoroFP32ArchiveSize = int64(len(fp32Archive))

	KokoroINT8ArchiveURL = server.URL + "/int8.tar.bz2"
	KokoroINT8ArchiveSHA256 = hex.EncodeToString(int8Sum[:])
	KokoroINT8ArchiveSize = int64(len(int8Archive))

	destDir := t.TempDir()
	if err := DownloadKokoroModelForMode(destDir, false, nil); err != nil {
		t.Fatalf("DownloadKokoroModelForMode(useGPU=false) failed: %v", err)
	}
	if !IsModelInstalled(destDir) {
		t.Fatal("expected both CPU and GPU model assets to be installed in unified pack")
	}
	if fp32Hits.Load() != 1 || int8Hits.Load() != 1 {
		t.Errorf("expected fp32Hits=1, int8Hits=1; got fp32=%d, int8=%d", fp32Hits.Load(), int8Hits.Load())
	}
}

func TestMultiModelPresetsAndHardwareImpact(t *testing.T) {
	if fam := VoiceFamily("af_bella"); fam != ModelKokoro {
		t.Errorf("expected kokoro for af_bella, got %q", fam)
	}
	if fam := VoiceFamily("supertonic:m2"); fam != ModelSupertonic {
		t.Errorf("expected supertonic for supertonic:m2, got %q", fam)
	}
	if fam := VoiceFamily("Supertonic M2 — Deep US Male (supertonic:m2)"); fam != ModelSupertonic {
		t.Errorf("expected supertonic from display label, got %q", fam)
	}
	if fam := VoiceFamily("kitten_mini:f3"); fam != ModelKittenMini {
		t.Errorf("expected kitten_mini, got %q", fam)
	}
	if fam := VoiceFamily("kitten_nano:m1"); fam != ModelKittenNano {
		t.Errorf("expected kitten_nano, got %q", fam)
	}
	if fam := VoiceFamily("piper:spk_10"); fam != ModelPiper {
		t.Errorf("expected piper, got %q", fam)
	}

	if sid := ResolveFamilySpeakerID(ModelSupertonic, "supertonic:m2", 10); sid != 6 {
		t.Errorf("expected sid 6 for supertonic:m2, got %d", sid)
	}
	if sid := ResolveFamilySpeakerID(ModelKittenMini, "kitten_mini:m4", 8); sid != 6 {
		t.Errorf("expected sid 6 for kitten_mini:m4, got %d", sid)
	}
	if sid := ResolveFamilySpeakerID(ModelPiper, "piper:spk_42", 904); sid != 42 {
		t.Errorf("expected sid 42 for piper:spk_42, got %d", sid)
	}

	labels := VoiceDisplayLabelsForModels([]string{ModelSupertonic, ModelKittenNano})
	expectedCount := len(SupertonicVoiceCatalog) + len(KittenNanoVoiceCatalog)
	if len(labels) != expectedCount {
		t.Errorf("expected %d labels for supertonic+kitten_nano, got %d", expectedCount, len(labels))
	}

	pFast := PresetByID(PresetFastLean)
	if pFast.ID != PresetFastLean || len(pFast.EnabledModels) != 1 || pFast.EnabledModels[0] != ModelSupertonic {
		t.Errorf("unexpected fast_lean preset: %+v", pFast)
	}

	cpuImpact := EstimateHardwareImpact([]string{ModelKokoro}, false, 4)
	gpuImpact := EstimateHardwareImpact([]string{ModelKokoro}, true, 4)
	superImpact := EstimateHardwareImpact([]string{ModelSupertonic}, false, 4)

	if cpuImpact.ActiveRAMMB != 710 {
		t.Errorf("expected CPU Kokoro ActiveRAMMB == 710 MB, got %d", cpuImpact.ActiveRAMMB)
	}
	if gpuImpact.ActiveRAMMB != 1300 {
		t.Errorf("expected GPU Kokoro ActiveRAMMB == 1300 MB, got %d", gpuImpact.ActiveRAMMB)
	}
	if superImpact.ActiveRAMMB != 600 {
		t.Errorf("expected Supertonic ActiveRAMMB == 600 MB, got %d", superImpact.ActiveRAMMB)
	}
	if cpuImpact.ActiveRAMMB >= gpuImpact.ActiveRAMMB {
		t.Errorf("expected CPU Kokoro RAM (%d MB) < GPU Kokoro RAM (%d MB)", cpuImpact.ActiveRAMMB, gpuImpact.ActiveRAMMB)
	}
	if superImpact.LatencyMeter >= cpuImpact.LatencyMeter {
		t.Errorf("expected Supertonic CPU latency (%f) < Kokoro CPU latency (%f)", superImpact.LatencyMeter, cpuImpact.LatencyMeter)
	}
	if gpuImpact.GPUMeter <= 0 {
		t.Errorf("expected positive GPUMeter when UseGPU=true with Kokoro, got %f", gpuImpact.GPUMeter)
	}
	if cpuImpact.TradeoffNotice == "" {
		t.Error("expected non-empty TradeoffNotice")
	}

	multiImpact := EstimateHardwareImpact([]string{ModelSupertonic, ModelKittenNano}, false, 4)
	if len(multiImpact.RAMSegments) != 3 { // base + supertonic + kitten_nano
		t.Errorf("expected 3 RAM segments for 2 enabled models, got %d", len(multiImpact.RAMSegments))
	}
	if multiImpact.RAMSegments[0].Label != "Base App: 450 MB" {
		t.Errorf("expected base segment label 'Base App: 450 MB', got %q", multiImpact.RAMSegments[0].Label)
	}
	if multiImpact.ActiveRAMMB != 450+150+95 {
		t.Errorf("expected multiImpact.ActiveRAMMB == %d, got %d", 450+150+95, multiImpact.ActiveRAMMB)
	}
	if len(multiImpact.CPUSegments) != 2 {
		t.Errorf("expected 2 CPU segments for 2 enabled models, got %d", len(multiImpact.CPUSegments))
	}
	if multiImpact.CPUSegments[1].Fraction <= multiImpact.CPUSegments[0].Fraction*2.5 {
		t.Errorf("expected Kitten Nano CPU segment (%f) to be ~3x Supertonic CPU segment (%f)",
			multiImpact.CPUSegments[1].Fraction, multiImpact.CPUSegments[0].Fraction)
	}
}

func TestFindVoiceUsagesAndReplacements(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.GlobalChatRules.DefaultVoice = "supertonic:f1"
	cfg.GlobalChatRules.DefaultSpeed = 1.15
	cfg.Viewers["twitch:alice"] = config.ViewerConfig{
		Platform: "twitch",
		UserID:   "alice",
		Voice:    "kitten_nano:f4",
		Speed:    1.25,
	}
	cfg.Viewers["twitch:bob"] = config.ViewerConfig{
		Platform: "twitch",
		UserID:   "bob",
		Nickname: "Bobby",
		Voice:    "kitten_nano:m1",
		Speed:    1.0,
	}
	twitchCfg := cfg.Platforms["twitch"]
	twitchCfg.Overrides.VoiceOverrideMode = config.OverrideModeCustom
	twitchCfg.Overrides.DefaultVoice = "supertonic:m2"
	twitchCfg.Overrides.DefaultSpeed = 1.10
	if twitchCfg.Notifications == nil {
		twitchCfg.Notifications = make(map[string]config.NotificationEventConfig)
	}
	twitchCfg.Notifications["sub"] = config.NotificationEventConfig{
		Enabled:  true,
		Template: "{user} subscribed!",
		Voice:    "kitten_nano:f2",
	}
	cfg.Platforms["twitch"] = twitchCfg

	usages := FindVoiceUsages(cfg, []string{ModelSupertonic, ModelKittenNano})
	if len(usages) != 5 {
		t.Fatalf("expected 5 voice usages, got %d: %+v", len(usages), usages)
	}

	warning := FormatVoiceUsagesWarning(usages)
	if !strings.Contains(warning, "Global Default Voice") || !strings.Contains(warning, "alice") || !strings.Contains(warning, "Twitch Platform Default Voice Override") {
		t.Errorf("expected warning to list Global Default Voice, alice, and Twitch override, got: %s", warning)
	}

	disabled := DisabledModelFamilies([]string{ModelSupertonic, ModelKittenNano}, []string{ModelSupertonic})
	if len(disabled) != 1 || disabled[0] != ModelKittenNano {
		t.Errorf("expected disabled=[kitten_nano], got %v", disabled)
	}

	// DefaultReplacementVoice prefers global default if it belongs to remainingModels
	if got := DefaultReplacementVoice(cfg, []string{ModelSupertonic}); got != "supertonic:f1" {
		t.Errorf("expected DefaultReplacementVoice to keep supertonic:f1 when supertonic remains enabled, got %q", got)
	}
	// Otherwise picks the first voice of remainingModels
	if got := DefaultReplacementVoice(cfg, []string{ModelKokoro}); got != "af" {
		t.Errorf("expected DefaultReplacementVoice for kokoro to be af, got %q", got)
	}

	// Test ApplyVoiceUsageReplacements:
	// - Reassign Global Default Voice to af_bella @ 1.2x
	// - Clear alice viewer override (no nickname -> removed from cfg.Viewers)
	// - Clear bob viewer override (has nickname -> kept in cfg.Viewers with empty Voice)
	// - Clear Twitch Platform Override (resets to global)
	// - Reassign Twitch sub alert to am_michael
	replacements := []VoiceReplacement{
		{VoiceID: "af_bella", Speed: 1.20},
		{VoiceID: "", Speed: 1.25},
		{VoiceID: "", Speed: 1.0},
		{VoiceID: "", Speed: 1.10},
		{VoiceID: "am_michael", Speed: 1.0},
	}
	ApplyVoiceUsageReplacements(cfg, usages, replacements)

	if cfg.GlobalChatRules.DefaultVoice != "af_bella" || cfg.GlobalChatRules.DefaultSpeed != 1.20 {
		t.Errorf("expected global voice af_bella @ 1.20, got %q @ %v", cfg.GlobalChatRules.DefaultVoice, cfg.GlobalChatRules.DefaultSpeed)
	}
	if _, exists := cfg.Viewers["twitch:alice"]; exists {
		t.Error("expected twitch:alice viewer override with no nickname/flags to be deleted when cleared")
	}
	if bob, exists := cfg.Viewers["twitch:bob"]; !exists || bob.Voice != "" || bob.Nickname != "Bobby" {
		t.Errorf("expected twitch:bob to remain with Nickname='Bobby' and Voice='', got exists=%v %+v", exists, bob)
	}
	updatedTwitch := cfg.Platforms["twitch"]
	if updatedTwitch.Overrides.VoiceOverrideMode != config.OverrideModeGlobal || updatedTwitch.Overrides.DefaultVoice != "" {
		t.Errorf("expected Twitch platform voice override to reset to global, got mode=%q voice=%q",
			updatedTwitch.Overrides.VoiceOverrideMode, updatedTwitch.Overrides.DefaultVoice)
	}
	if updatedTwitch.Notifications["sub"].Voice != "am_michael" {
		t.Errorf("expected Twitch sub alert voice 'am_michael', got %q", updatedTwitch.Notifications["sub"].Voice)
	}
}

func TestKokoroV10VoicesAndIntlPack(t *testing.T) {
	if len(KokoroVoiceCatalog) != 29 {
		t.Errorf("expected 29 voices in KokoroVoiceCatalog (28 named + 1 blend), got %d", len(KokoroVoiceCatalog))
	}
	if len(KokoroIntlVoiceCatalog) != 25 {
		t.Errorf("expected 25 voices in KokoroIntlVoiceCatalog, got %d", len(KokoroIntlVoiceCatalog))
	}
	if len(KnownKokoroV10Voices) != 54 {
		t.Errorf("expected 54 entries in KnownKokoroV10Voices (53 speakers + 1 blend), got %d", len(KnownKokoroV10Voices))
	}

	testCases := []struct {
		voiceID string
		wantSID int
		wantFam string
	}{
		{"af_heart", 3, ModelKokoro},
		{"af_alloy", 0, ModelKokoro},
		{"af_aoede", 1, ModelKokoro},
		{"af_bella", 2, ModelKokoro},
		{"af_nova", 7, ModelKokoro},
		{"am_fenrir", 14, ModelKokoro},
		{"am_puck", 18, ModelKokoro},
		{"bm_lewis", 27, ModelKokoro},
		{"kokoro_intl:ef_dora", 28, ModelKokoroIntl},
		{"kokoro_intl:em_alex", 29, ModelKokoroIntl},
		{"kokoro_intl:ff_siwis", 31, ModelKokoroIntl},
		{"kokoro_intl:hf_alpha", 32, ModelKokoroIntl},
		{"kokoro_intl:if_sara", 36, ModelKokoroIntl},
		{"kokoro_intl:jf_alpha", 38, ModelKokoroIntl},
		{"kokoro_intl:pf_dora", 43, ModelKokoroIntl},
		{"kokoro_intl:zf_xiaobei", 46, ModelKokoroIntl},
		{"kokoro_intl:zm_yunyang", 52, ModelKokoroIntl},
	}

	for _, tc := range testCases {
		if fam := VoiceFamily(tc.voiceID); fam != tc.wantFam {
			t.Errorf("VoiceFamily(%q) = %q, want %q", tc.voiceID, fam, tc.wantFam)
		}
		if sid := ResolveFamilySpeakerID(tc.wantFam, tc.voiceID, 53); sid != tc.wantSID {
			t.Errorf("ResolveFamilySpeakerID(%q, %q, 53) = %d, want %d", tc.wantFam, tc.voiceID, sid, tc.wantSID)
		}
	}

	if fam := VoiceFamily("ef_dora"); fam != ModelKokoroIntl {
		t.Errorf("expected kokoro_intl for raw ef_dora, got %q", fam)
	}
	if sid := ResolveFamilySpeakerID(ModelKokoroIntl, "ef_dora", 53); sid != 28 {
		t.Errorf("expected sid 28 for raw ef_dora, got %d", sid)
	}

	if !IsKokoroIntlVoice("kokoro_intl:ef_dora") {
		t.Error("expected true for kokoro_intl:ef_dora")
	}
	if !IsKokoroIntlVoice("ef_dora") {
		t.Error("expected true for raw ef_dora")
	}
	if IsKokoroIntlVoice("af_nova") {
		t.Error("expected false for af_nova")
	}
	if IsKokoroIntlVoice("supertonic:f1") {
		t.Error("expected false for supertonic:f1")
	}
}

func TestKokoroIntlHardwareImpactSharing(t *testing.T) {
	singleImpact := EstimateHardwareImpact([]string{ModelKokoro}, false, 4)
	bothImpact := EstimateHardwareImpact([]string{ModelKokoro, ModelKokoroIntl}, false, 4)

	if bothImpact.ActiveRAMMB != singleImpact.ActiveRAMMB {
		t.Errorf("expected both ActiveRAMMB (%d) == single ActiveRAMMB (%d)", bothImpact.ActiveRAMMB, singleImpact.ActiveRAMMB)
	}
	if bothImpact.IdleRAMMB != singleImpact.IdleRAMMB {
		t.Errorf("expected both IdleRAMMB (%d) == single IdleRAMMB (%d)", bothImpact.IdleRAMMB, singleImpact.IdleRAMMB)
	}
	if !strings.Contains(bothImpact.RAMBarText, "Kokoro Intl +0 MB (shared)") {
		t.Errorf("expected RAMBarText to mention shared +0 MB, got: %s", bothImpact.RAMBarText)
	}
}

func TestLegacyKokoroV019Detection(t *testing.T) {
	dir := t.TempDir()

	if isLegacyKokoroV019Dir(dir) {
		t.Error("expected false for empty dir")
	}

	_ = os.WriteFile(filepath.Join(dir, "voices.bin"), []byte("test"), 0600)
	if isLegacyKokoroV019Dir(dir) {
		t.Error("expected false for small test mock voices.bin")
	}

	staleData := make([]byte, 5750784)
	_ = os.WriteFile(filepath.Join(dir, "voices.bin"), staleData, 0600)
	if !isLegacyKokoroV019Dir(dir) {
		t.Error("expected true for 5.75MB voices.bin without lexicon")
	}
	if hasKokoroSharedAssets(dir) {
		t.Error("expected false hasKokoroSharedAssets for legacy v0.19 dir")
	}

	_ = os.WriteFile(filepath.Join(dir, "lexicon-us-en.txt"), []byte("hello"), 0600)
	if isLegacyKokoroV019Dir(dir) {
		t.Error("expected false once lexicon-us-en.txt exists")
	}
}

func TestDefaultModelDir(t *testing.T) {
	dir := DefaultModelDir()
	if dir == "" {
		t.Fatal("expected non-empty DefaultModelDir")
	}
	if !filepath.IsAbs(dir) {
		t.Errorf("expected absolute path for DefaultModelDir, got %s", dir)
	}
	if !strings.Contains(dir, "com.github.mrpoundsign.towncrier") {
		t.Errorf("expected path to contain application ID com.github.mrpoundsign.towncrier, got %s", dir)
	}
}

func TestMigrateModelsDir(t *testing.T) {
	baseTmp := t.TempDir()
	legacyModels := filepath.Join(baseTmp, "roaming", "models")
	legacyKokoro := filepath.Join(legacyModels, "kokoro-en")
	_ = os.MkdirAll(legacyKokoro, 0755)

	_ = os.WriteFile(filepath.Join(legacyKokoro, "model.int8.onnx"), []byte("int8"), 0600)
	_ = os.WriteFile(filepath.Join(legacyKokoro, "model.onnx"), []byte("fp32"), 0600)
	_ = os.WriteFile(filepath.Join(legacyKokoro, "voices.bin"), []byte("voices"), 0600)
	_ = os.WriteFile(filepath.Join(legacyKokoro, "tokens.txt"), []byte("tokens"), 0600)
	_ = os.MkdirAll(filepath.Join(legacyKokoro, "espeak-ng-data"), 0755)

	targetModels := filepath.Join(baseTmp, "local", "models")
	targetKokoro := filepath.Join(targetModels, "kokoro-en")

	if !migrateModelsDir(legacyModels, targetModels) {
		t.Fatal("migrateModelsDir returned false")
	}

	if !IsModelInstalled(targetKokoro) {
		t.Errorf("expected model to be installed at target %s", targetKokoro)
	}

	if _, err := os.Stat(legacyModels); !os.IsNotExist(err) {
		t.Errorf("expected legacy models dir to be removed after migration")
	}
}



