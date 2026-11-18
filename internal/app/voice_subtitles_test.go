package app

import (
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mrpoundsign/TownCrier/internal/ws"
	"github.com/mrpoundsign/TownCrier/pkg/config"
	"github.com/mrpoundsign/TownCrier/pkg/secrets"
	"github.com/mrpoundsign/TownCrier/pkg/stt"
)

type mockSTTEngine struct {
	text string
	lang string
}

func (m *mockSTTEngine) Transcribe(samples []float32, _ int, _ []string) (stt.Transcript, error) {
	if len(samples) == 0 {
		return stt.Transcript{}, nil
	}
	return stt.Transcript{
		Text:         m.text,
		RawText:      m.text,
		DetectedLang: m.lang,
	}, nil
}

func (m *mockSTTEngine) ModelFamily() string               { return stt.ModelWhisperTurbo }
func (m *mockSTTEngine) Provider() string                  { return "cpu" }
func (m *mockSTTEngine) SetIdleTimeout(_ time.Duration)    {}
func (m *mockSTTEngine) Close() error                      { return nil }

type mockSTTSegmenter struct {
	cfg        stt.SegmenterConfig
	resetCount atomic.Int32
}

func (m *mockSTTSegmenter) FeedSamples(samples []float32) {
	if m.cfg.OnSpeechState != nil {
		m.cfg.OnSpeechState(true)
	}
	if m.cfg.OnSegment != nil {
		m.cfg.OnSegment(samples)
	}
	if m.cfg.OnSpeechState != nil {
		m.cfg.OnSpeechState(false)
	}
}

func (m *mockSTTSegmenter) Flush()         {}
func (m *mockSTTSegmenter) Reset()         { m.resetCount.Add(1) }
func (m *mockSTTSegmenter) IsSpeech() bool { return false }
func (m *mockSTTSegmenter) Close() error   { return nil }

func TestSessionVoiceSubtitlesPipelineAndPanicClear(t *testing.T) {
	mtServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lp := r.URL.Query().Get("langpair")
		var out string
		switch lp {
		case "tl|ja":
			out = "皆さん、こんばんは！"
		case "tl|en":
			out = "Good evening everyone!"
		default:
			out = "tx:" + lp
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"responseData":{"translatedText":"` + out + `","match":1.0},"responseStatus":200}`))
	}))
	defer mtServer.Close()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("allocate ephemeral port: %v", err)
	}
	freePort := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	cfg := config.DefaultConfig()
	cfg.TTS.Enabled = false     // Verify Voice Subtitles & Mic Capture work even when Chat TTS is completely disabled!
	cfg.Logging.Enabled = false // Verify OBS server runs with only SubtitlesOverlayEnabled
	cfg.WebSocket.Port = freePort
	cfg.WebSocket.SubtitlesOverlayEnabled = true
	cfg.VoiceSubtitles.Enabled = true
	cfg.VoiceSubtitles.SpokenLanguages = []string{"en", "tl"}
	cfg.VoiceSubtitles.TargetLanguages = []string{"tl", "ja", "en"}

	memStore := config.NewMemoryStore(cfg)
	sess := NewSession(cfg, secrets.NewMemoryStore(), memStore)
	defer func() { _ = sess.Close() }()

	sess.SetSTTModelDir(t.TempDir())
	sess.Translator().SetHTTPClient(mtServer.Client())
	sess.Translator().SetCloudEndpoints(mtServer.URL, mtServer.URL, mtServer.URL)

	var activeSeg *mockSTTSegmenter
	var segMu sync.Mutex
	sess.SetSTTFactories(
		func(_, _ string, _ int, _ bool) (stt.Engine, error) {
			return &mockSTTEngine{
				text: "Magandang gabi po sa inyong lahat!",
				lang: "tl",
			}, nil
		},
		func(segCfg stt.SegmenterConfig) (stt.Segmenter, error) {
			segMu.Lock()
			activeSeg = &mockSTTSegmenter{cfg: segCfg}
			s := activeSeg
			segMu.Unlock()
			return s, nil
		},
	)

	subCh := make(chan ws.SubtitleEvent, 8)
	sess.SetOnVoiceSubtitle(func(ev ws.SubtitleEvent) {
		subCh <- ev
	})

	if err := sess.Start(); err != nil {
		t.Fatalf("Session.Start() failed: %v", err)
	}

	if !sess.MicMonitor().IsRunning() {
		t.Fatal("expected CaptureMonitor to start when VoiceSubtitles.Enabled=true even though TTS.Enabled=false")
	}

	// Simulate captured 16 kHz audio buffer arriving on CaptureMonitor
	sess.MicMonitor().ProcessSamples([]float32{0.25, -0.35, 0.45, -0.20}, time.Now())

	var gotSub ws.SubtitleEvent
	select {
	case gotSub = <-subCh:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for multi-language voice subtitle event")
	}

	if gotSub.Kind != "subtitle" || len(gotSub.Lines) != 3 {
		t.Fatalf("unexpected subtitle event: %+v", gotSub)
	}
	if gotSub.Lines[0].Lang != "tl" || gotSub.Lines[0].Label != "FIL" || !gotSub.Lines[0].IsOriginal {
		t.Errorf("unexpected line[0] (FIL): %+v", gotSub.Lines[0])
	}
	if gotSub.Lines[1].Lang != "ja" || gotSub.Lines[1].Text != "皆さん、こんばんは！" {
		t.Errorf("unexpected line[1] (JA): %+v", gotSub.Lines[1])
	}
	if gotSub.Lines[2].Lang != "en" || gotSub.Lines[2].Text != "Good evening everyone!" {
		t.Errorf("unexpected line[2] (EN): %+v", gotSub.Lines[2])
	}

	snap := sess.SnapshotOverlaySubtitles()
	if snap.Latest == nil || len(snap.Latest.Lines) != 3 {
		t.Fatalf("expected SnapshotOverlaySubtitles().Latest to hold 3 lines, got %+v", snap.Latest)
	}

	// Trigger Emergency Panic Mute -> must immediately clear active subtitle and reset VAD segmenter
	sess.Panic()

	var gotClear ws.SubtitleEvent
	select {
	case gotClear = <-subCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for panic clear subtitle event")
	}
	if gotClear.Kind != "clear" {
		t.Fatalf("expected kind='clear' on Panic(), got %q", gotClear.Kind)
	}
	if sess.SnapshotOverlaySubtitles().Latest != nil {
		t.Fatal("expected SnapshotOverlaySubtitles().Latest to be nil after Panic()")
	}
	segMu.Lock()
	resets := activeSeg.resetCount.Load()
	segMu.Unlock()
	if resets == 0 {
		t.Fatal("expected segmenter.Reset() to be called on Panic()")
	}
}
