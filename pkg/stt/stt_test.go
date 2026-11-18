package stt

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
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
