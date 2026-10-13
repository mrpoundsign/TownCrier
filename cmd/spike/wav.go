package main

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"math"
	"os"
)

// WriteWav writes 32-bit float samples (normalized -1.0 to 1.0) as 16-bit mono PCM WAV
func WriteWav(filename string, samples []float32, sampleRate int) (err error) {
	file, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	defer func() {
		if cerr := file.Close(); err == nil && cerr != nil {
			err = fmt.Errorf("close file: %w", cerr)
		}
	}()

	w := bufio.NewWriter(file)

	numSamples := len(samples)
	numChannels := uint16(1)
	bitsPerSample := uint16(16)
	bytesPerSample := bitsPerSample / 8
	blockAlign := numChannels * bytesPerSample
	byteRate := uint32(sampleRate) * uint32(blockAlign)
	dataSize := uint32(numSamples * int(bytesPerSample))
	chunkSize := 36 + dataSize

	if _, err := w.WriteString("RIFF"); err != nil {
		return err
	}
	if err := binary.Write(w, binary.LittleEndian, chunkSize); err != nil {
		return err
	}
	if _, err := w.WriteString("WAVE"); err != nil {
		return err
	}
	if _, err := w.WriteString("fmt "); err != nil {
		return err
	}
	subchunk1Size := uint32(16)
	audioFormat := uint16(1)
	if err := binary.Write(w, binary.LittleEndian, subchunk1Size); err != nil {
		return err
	}
	if err := binary.Write(w, binary.LittleEndian, audioFormat); err != nil {
		return err
	}
	if err := binary.Write(w, binary.LittleEndian, numChannels); err != nil {
		return err
	}
	if err := binary.Write(w, binary.LittleEndian, uint32(sampleRate)); err != nil {
		return err
	}
	if err := binary.Write(w, binary.LittleEndian, byteRate); err != nil {
		return err
	}
	if err := binary.Write(w, binary.LittleEndian, blockAlign); err != nil {
		return err
	}
	if err := binary.Write(w, binary.LittleEndian, bitsPerSample); err != nil {
		return err
	}
	if _, err := w.WriteString("data"); err != nil {
		return err
	}
	if err := binary.Write(w, binary.LittleEndian, dataSize); err != nil {
		return err
	}

	buf := make([]byte, numSamples*2)
	for i, s := range samples {
		if s > 1.0 {
			s = 1.0
		} else if s < -1.0 {
			s = -1.0
		}
		intSample := uint16(int16(math.Round(float64(s * 32767.0))))
		binary.LittleEndian.PutUint16(buf[i*2:], intSample)
	}

	if _, err := w.Write(buf); err != nil {
		return err
	}

	if err := w.Flush(); err != nil {
		return fmt.Errorf("flush writer: %w", err)
	}

	return nil
}
