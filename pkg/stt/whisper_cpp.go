package stt

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	whisperHelperMagic uint32 = 0x54435753 // "TCWS"
	maxHelperJSONBytes uint32 = 4 << 20
)

type helperFrameResponse struct {
	OK       bool   `json:"ok"`
	Ready    bool   `json:"ready,omitempty"`
	Provider string `json:"provider,omitempty"`
	Text     string `json:"text,omitempty"`
	Lang     string `json:"lang,omitempty"`
	DecodeMs int64  `json:"decode_ms,omitempty"`
	Error    string `json:"error,omitempty"`
}

// WhisperCppEngine runs quantized GGML Whisper models on Vulkan GPUs/iGPUs (NVIDIA, AMD, Intel) or CPU
// via the bundled towncrier-whisper helper process over binary stdio pipes.
type WhisperCppEngine struct {
	mu            sync.Mutex
	procMu        sync.Mutex
	family        string
	baseSTTDir    string
	helperPath    string
	computeDevice string
	numThreads    int
	provider      string
	providerAtom  atomic.Value
	idleTimeout   time.Duration
	idleTimer     *time.Timer
	cmd           *exec.Cmd
	stdin         io.WriteCloser
	stdout        io.ReadCloser
	stderrBuf     bytes.Buffer
	closed        atomic.Bool
}

// NewBestEngine creates the optimal offline Whisper Engine for the installed model files and hardware preferences.
// It prefers WhisperCppEngine (Vulkan dGPU/iGPU on quantized GGML weights) when GPU/iGPU compute is requested and
// falls back to SherpaWhisperEngine (ONNX Runtime INT8 MLAS kernels) when CPU-only mode is selected and ONNX INT8
// weights are installed, or when only legacy ONNX CPU weights exist.
func NewBestEngine(family string, baseSTTDir string, numThreads int, computeDevice string) (Engine, error) {
	normFamily := NormalizeModelFamily(family)
	if strings.TrimSpace(baseSTTDir) == "" {
		baseSTTDir = DefaultSTTModelDir()
	}
	normDev := NormalizeComputeDevice(computeDevice)

	// When CPU-only mode is explicitly selected and ONNX Runtime INT8 weights are installed,
	// SherpaWhisperEngine's MLAS INT8 AVX2/VNNI kernels are significantly faster on CPU than
	// decoding GPU-quantized Q5 GGML weights without Vulkan.
	if normDev == ComputeDeviceCPU && IsSherpaModelInstalled(baseSTTDir, normFamily) {
		cpuThreads := SmartSTTThreads(numThreads, runtime.NumCPU(), false)
		if eng, sErr := NewSherpaWhisperEngine(normFamily, baseSTTDir, cpuThreads, false); sErr == nil {
			return eng, nil
		}
	}

	if IsGGMLModelInstalled(baseSTTDir, normFamily) {
		if helperPath, err := ResolveHelperBinaryPath(); err == nil && helperPath != "" {
			eng, wErr := NewWhisperCppEngine(normFamily, baseSTTDir, helperPath, numThreads, normDev)
			if wErr == nil {
				return eng, nil
			}
			log.Printf("[STT] WhisperCppEngine (%s, device=%s) failed to start (%v); checking ONNX fallback...", normFamily, normDev, wErr)
			if !IsSherpaModelInstalled(baseSTTDir, normFamily) {
				return nil, wErr
			}
		} else if !IsSherpaModelInstalled(baseSTTDir, normFamily) {
			return nil, fmt.Errorf("locate whisper Vulkan/CPU engine: %w", err)
		}
	}

	cpuThreads := SmartSTTThreads(numThreads, runtime.NumCPU(), false)
	return NewSherpaWhisperEngine(normFamily, baseSTTDir, cpuThreads, false)
}

// NewWhisperCppEngine initializes a WhisperCppEngine backed by towncrier-whisper.
func NewWhisperCppEngine(family, baseSTTDir, helperPath string, numThreads int, computeDevice string) (*WhisperCppEngine, error) {
	normFamily := NormalizeModelFamily(family)
	if strings.TrimSpace(baseSTTDir) == "" {
		baseSTTDir = DefaultSTTModelDir()
	}
	if strings.TrimSpace(helperPath) == "" {
		var err error
		helperPath, err = ResolveHelperBinaryPath()
		if err != nil {
			return nil, err
		}
	}
	modelPath, err := GGMLModelPath(baseSTTDir, normFamily)
	if err != nil || !fileExistsNonEmpty(modelPath) {
		return nil, fmt.Errorf("%w: missing GGML Whisper model for %s in %s", ErrModelNotFound, normFamily, baseSTTDir)
	}

	e := &WhisperCppEngine{
		family:        normFamily,
		baseSTTDir:    baseSTTDir,
		helperPath:    helperPath,
		computeDevice: NormalizeComputeDevice(computeDevice),
		numThreads:    numThreads,
	}

	e.mu.Lock()
	startErr := e.ensureProcessLocked(false)
	e.mu.Unlock()
	if startErr != nil {
		return nil, startErr
	}
	return e, nil
}

func readHelperJSONFrame(r io.Reader) (helperFrameResponse, error) {
	var frameLen uint32
	if err := binary.Read(r, binary.LittleEndian, &frameLen); err != nil {
		return helperFrameResponse{}, err
	}
	if frameLen == 0 || frameLen > maxHelperJSONBytes {
		return helperFrameResponse{}, fmt.Errorf("invalid helper frame length %d", frameLen)
	}
	buf := make([]byte, frameLen)
	if _, err := io.ReadFull(r, buf); err != nil {
		return helperFrameResponse{}, err
	}
	var resp helperFrameResponse
	if err := json.Unmarshal(buf, &resp); err != nil {
		return helperFrameResponse{}, err
	}
	return resp, nil
}

func filterEnvWithoutKey(env []string, key string) []string {
	prefix := strings.ToUpper(key) + "="
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if !strings.HasPrefix(strings.ToUpper(kv), prefix) {
			out = append(out, kv)
		}
	}
	return out
}

func (e *WhisperCppEngine) ensureProcessLocked(forceCPU bool) error {
	if e.closed.Load() {
		return ErrEngineClosed
	}
	e.procMu.Lock()
	if e.cmd != nil && e.stdin != nil && e.stdout != nil {
		e.procMu.Unlock()
		return nil
	}
	e.procMu.Unlock()

	modelPath, err := GGMLModelPath(e.baseSTTDir, e.family)
	if err != nil || !fileExistsNonEmpty(modelPath) {
		return fmt.Errorf("%w: missing GGML Whisper model at %s", ErrModelNotFound, modelPath)
	}

	var chosenGPU *GPUDevice
	useGPU := false
	if !forceCPU && e.computeDevice != ComputeDeviceCPU {
		if devs, devErr := EnumerateGPUDevices(); devErr == nil && len(devs) > 0 {
			chosenGPU, useGPU = SelectGPUDevice(e.computeDevice, devs)
		}
	}

	effThreads := SmartSTTThreads(e.numThreads, runtime.NumCPU(), useGPU)

	args := []string{
		"--model", modelPath,
		"--threads", strconv.Itoa(effThreads),
	}

	env := filterEnvWithoutKey(os.Environ(), "GGML_VK_VISIBLE_DEVICES")
	switch {
	case !useGPU || chosenGPU == nil:
		args = append(args, "--no-gpu")
	case chosenGPU.VKIndex >= 0:
		// Pin Vulkan visibility exclusively to the selected adapter so integrated iGPU mode
		// never opens a Vulkan context or allocates VRAM on the discrete gaming GPU.
		env = append(env, fmt.Sprintf("GGML_VK_VISIBLE_DEVICES=%d", chosenGPU.VKIndex))
		args = append(args, "--gpu-device", "0")
	default:
		args = append(args, "--gpu-device", strconv.Itoa(chosenGPU.GPUIndex))
	}

	cmd := exec.Command(e.helperPath, args...)
	cmd.Env = env
	configureSubprocess(cmd)

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("open whisper helper stdin: %w", err)
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdinPipe.Close()
		return fmt.Errorf("open whisper helper stdout: %w", err)
	}
	e.stderrBuf.Reset()
	cmd.Stderr = &e.stderrBuf

	if err := cmd.Start(); err != nil {
		_ = stdinPipe.Close()
		_ = stdoutPipe.Close()
		return fmt.Errorf("start whisper helper: %w", err)
	}

	type readyResult struct {
		resp helperFrameResponse
		err  error
	}
	readyCh := make(chan readyResult, 1)
	go func() {
		r, rErr := readHelperJSONFrame(stdoutPipe)
		readyCh <- readyResult{resp: r, err: rErr}
	}()

	select {
	case res := <-readyCh:
		if res.err != nil || !res.resp.OK {
			_ = stdinPipe.Close()
			_ = stdoutPipe.Close()
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			if useGPU && !forceCPU {
				log.Printf("[STT] Vulkan GPU helper init failed (%v / %s; stderr=%s) — retrying on CPU...",
					res.err, res.resp.Error, strings.TrimSpace(e.stderrBuf.String()))
				return e.ensureProcessLocked(true)
			}
			if res.resp.Error != "" {
				return fmt.Errorf("whisper helper init error: %s", res.resp.Error)
			}
			return fmt.Errorf("whisper helper startup failed: %w (stderr: %s)", res.err, strings.TrimSpace(e.stderrBuf.String()))
		}
		e.procMu.Lock()
		if e.closed.Load() {
			e.procMu.Unlock()
			_ = stdinPipe.Close()
			_ = stdoutPipe.Close()
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return ErrEngineClosed
		}
		e.cmd = cmd
		e.stdin = stdinPipe
		e.stdout = stdoutPipe
		e.procMu.Unlock()
		prov := strings.TrimSpace(res.resp.Provider)
		if prov == "" {
			prov = "cpu"
		}
		if prov == "cpu" {
			prov = fmt.Sprintf("cpu (%d threads)", effThreads)
		}
		e.provider = prov
		e.providerAtom.Store(prov)
		return nil

	case <-time.After(25 * time.Second):
		_ = stdinPipe.Close()
		_ = stdoutPipe.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		if useGPU && !forceCPU {
			log.Printf("[STT] Vulkan GPU shader/model init timed out — falling back to CPU...")
			return e.ensureProcessLocked(true)
		}
		return errors.New("timed out initializing whisper helper")
	}
}

func (e *WhisperCppEngine) stopProcessLocked() {
	e.procMu.Lock()
	stdin := e.stdin
	e.stdin = nil
	stdout := e.stdout
	e.stdout = nil
	cmd := e.cmd
	e.cmd = nil
	e.procMu.Unlock()

	if stdin != nil {
		_ = stdin.Close()
	}
	if stdout != nil {
		_ = stdout.Close()
	}
	if cmd != nil {
		done := make(chan struct{})
		go func() {
			_ = cmd.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(500 * time.Millisecond):
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			<-done
		}
	}
}

// ModelFamily returns the active Whisper model family ID.
func (e *WhisperCppEngine) ModelFamily() string {
	return e.family
}

// Provider returns the human-readable active compute device description (e.g. "vulkan:Intel(R) HD Graphics 630 (igpu)" or "cpu (2 threads)").
// Lock-free so UI status queries never block while an utterance is being decoded.
func (e *WhisperCppEngine) Provider() string {
	if v, ok := e.providerAtom.Load().(string); ok {
		return v
	}
	return ""
}

// SetIdleTimeout configures automatic helper shutdown after d of inactivity to release VRAM/RAM (0 disables).
func (e *WhisperCppEngine) SetIdleTimeout(d time.Duration) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.idleTimeout = d
	e.resetIdleTimerLocked()
}

func (e *WhisperCppEngine) resetIdleTimerLocked() {
	if e.idleTimer != nil {
		e.idleTimer.Stop()
		e.idleTimer = nil
	}
	e.procMu.Lock()
	hasCmd := e.cmd != nil
	e.procMu.Unlock()
	if e.idleTimeout > 0 && !e.closed.Load() && hasCmd {
		e.idleTimer = time.AfterFunc(e.idleTimeout, e.unloadIfIdle)
	}
}

func (e *WhisperCppEngine) unloadIfIdle() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.procMu.Lock()
	hasCmd := e.cmd != nil
	e.procMu.Unlock()
	if e.closed.Load() || !hasCmd {
		return
	}
	log.Printf("[STT] Unloading idle Whisper helper (%s / %s) after %v of inactivity to free VRAM/RAM...", e.family, e.provider, e.idleTimeout)
	e.stopProcessLocked()
}

func resampleLinearMono(samples []float32, srcRate, dstRate int) []float32 {
	if srcRate <= 0 || dstRate <= 0 || srcRate == dstRate || len(samples) == 0 {
		return samples
	}
	outLen := int(int64(len(samples)) * int64(dstRate) / int64(srcRate))
	if outLen <= 0 {
		return nil
	}
	out := make([]float32, outLen)
	ratio := float64(srcRate) / float64(dstRate)
	last := len(samples) - 1
	for i := range outLen {
		pos := float64(i) * ratio
		idx := int(pos)
		if idx >= last {
			out[i] = samples[last]
			continue
		}
		frac := float32(pos - float64(idx))
		out[i] = samples[idx]*(1.0-frac) + samples[idx+1]*frac
	}
	return out
}

func writeTranscribeRequest(w io.Writer, samples []float32, lang string, threads int32) error {
	langBytes := []byte(lang)
	hdr := make([]byte, 16+len(langBytes))
	binary.LittleEndian.PutUint32(hdr[0:4], whisperHelperMagic)
	binary.LittleEndian.PutUint32(hdr[4:8], uint32(len(samples)))
	binary.LittleEndian.PutUint32(hdr[8:12], uint32(len(langBytes)))
	binary.LittleEndian.PutUint32(hdr[12:16], uint32(threads))
	copy(hdr[16:], langBytes)
	if _, err := w.Write(hdr); err != nil {
		return err
	}

	pcmBytes := make([]byte, len(samples)*4)
	for i, s := range samples {
		binary.LittleEndian.PutUint32(pcmBytes[i*4:], math.Float32bits(s))
	}
	_, err := w.Write(pcmBytes)
	return err
}

// Transcribe decodes mono float32 PCM audio into cleaned text and resolved ISO-639-1 spoken language.
func (e *WhisperCppEngine) Transcribe(samples []float32, sampleRate int, spokenLangs []string) (Transcript, error) {
	if len(samples) == 0 {
		return Transcript{}, nil
	}
	if sampleRate <= 0 {
		sampleRate = DefaultSampleRate
	}
	audioDur := time.Duration(int64(len(samples)) * int64(time.Second) / int64(sampleRate))
	pcm16k := resampleLinearMono(samples, sampleRate, DefaultSampleRate)
	if len(pcm16k) == 0 {
		return Transcript{}, nil
	}

	normSpoken := NormalizeSpokenLanguageList(spokenLangs, nil)
	whisperLangHint := "auto"
	if len(normSpoken) == 1 {
		whisperLangHint = normSpoken[0]
	}

	start := time.Now()

	e.mu.Lock()
	if e.closed.Load() {
		e.mu.Unlock()
		return Transcript{}, ErrEngineClosed
	}
	if err := e.ensureProcessLocked(false); err != nil {
		e.mu.Unlock()
		return Transcript{}, err
	}

	usingGPU := strings.HasPrefix(strings.ToLower(e.provider), "vulkan:")
	effThreads := int32(SmartSTTThreads(e.numThreads, runtime.NumCPU(), usingGPU))

	resp, err := e.sendAndRecvLocked(pcm16k, whisperLangHint, effThreads)
	if err != nil && usingGPU && !e.closed.Load() {
		log.Printf("[STT] Vulkan decode error (%v); restarting Whisper helper on CPU...", err)
		e.stopProcessLocked()
		if cpuErr := e.ensureProcessLocked(true); cpuErr == nil {
			cpuThreads := int32(SmartSTTThreads(e.numThreads, runtime.NumCPU(), false))
			resp, err = e.sendAndRecvLocked(pcm16k, whisperLangHint, cpuThreads)
		}
	}
	activeProv := e.provider
	e.resetIdleTimerLocked()
	e.mu.Unlock()

	if err != nil {
		return Transcript{}, fmt.Errorf("%w: %v", ErrTranscribeFailed, err)
	}
	if !resp.OK {
		return Transcript{}, fmt.Errorf("%w: %s", ErrTranscribeFailed, resp.Error)
	}

	latency := time.Since(start)
	if resp.DecodeMs > 0 {
		latency = time.Duration(resp.DecodeMs) * time.Millisecond
	}

	rawText := strings.TrimSpace(resp.Text)
	cleaned, ok := CleanTranscript(rawText)
	if !ok {
		return Transcript{
			RawText:       rawText,
			AudioDuration: audioDur,
			Latency:       latency,
		}, nil
	}

	resolvedLang := ResolveSpokenLang(strings.TrimSpace(resp.Lang), cleaned, normSpoken)
	log.Printf("[STT] WhisperCpp (%s / %s, %dms audio, %dms decode): raw_lang=%q resolved=%q text=%q",
		e.family, activeProv, audioDur.Milliseconds(), latency.Milliseconds(), resp.Lang, resolvedLang, cleaned)

	return Transcript{
		Text:          cleaned,
		RawText:       rawText,
		DetectedLang:  resolvedLang,
		AudioDuration: audioDur,
		Latency:       latency,
	}, nil
}

func (e *WhisperCppEngine) sendAndRecvLocked(pcm16k []float32, lang string, threads int32) (helperFrameResponse, error) {
	e.procMu.Lock()
	stdin := e.stdin
	stdout := e.stdout
	e.procMu.Unlock()
	if stdin == nil || stdout == nil {
		return helperFrameResponse{}, ErrEngineClosed
	}
	if err := writeTranscribeRequest(stdin, pcm16k, lang, threads); err != nil {
		e.stopProcessLocked()
		return helperFrameResponse{}, err
	}
	resp, err := readHelperJSONFrame(stdout)
	if err != nil {
		e.stopProcessLocked()
		return helperFrameResponse{}, err
	}
	return resp, nil
}

// Close shuts down the background towncrier-whisper worker process immediately (even if a decode is in progress) and releases VRAM/RAM.
func (e *WhisperCppEngine) Close() error {
	if !e.closed.CompareAndSwap(false, true) {
		return nil
	}
	// Terminate the child process and close stdio pipes immediately without waiting for e.mu,
	// so any in-flight readHelperJSONFrame unblocks right away instead of hanging shutdown.
	e.stopProcessLocked()

	e.mu.Lock()
	if e.idleTimer != nil {
		e.idleTimer.Stop()
		e.idleTimer = nil
	}
	e.mu.Unlock()
	return nil
}
