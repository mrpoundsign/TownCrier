package pluginsdk

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"google.golang.org/protobuf/proto"
)

const (
	// ABIMajor is the plugin protocol major version implemented by this SDK.
	ABIMajor uint32 = 1
	// ABIMinor is the plugin protocol minor version implemented by this SDK.
	ABIMinor uint32 = 0
	// ABIPatch is the plugin protocol patch version implemented by this SDK.
	ABIPatch uint32 = 0

	// MaxFrameBytes bounds a single length-prefixed Protobuf frame across native TCP IPC.
	MaxFrameBytes = 16 * 1024 * 1024

	// EnvIPCAddr is the environment variable holding the host loopback TCP address for native plugins.
	EnvIPCAddr = "TOWNCRIER_IPC_ADDR"
	// EnvIPCToken is the environment variable holding the per-launch authentication token for native plugins.
	EnvIPCToken = "TOWNCRIER_IPC_TOKEN"
)

// PreambleMagic identifies the binary pre-Protobuf handshake header on native TCP connections.
var PreambleMagic = [4]byte{'T', 'C', 'R', 'P'}

// PackABIVersion packs (major, minHostMinor) into the 64-bit integer returned by tc_abi_version.
func PackABIVersion(major, minHostMinor uint32) uint64 {
	return (uint64(major) << 32) | uint64(minHostMinor)
}

// UnpackABIVersion extracts (major, minHostMinor) from a packed tc_abi_version return value.
func UnpackABIVersion(packed uint64) (major, minHostMinor uint32) {
	return uint32(packed >> 32), uint32(packed & 0xffffffff)
}

// WriteFrame writes a 4-byte big-endian length-prefixed Protobuf message to w.
func WriteFrame(w io.Writer, msg proto.Message) error {
	if w == nil {
		return errors.New("nil writer")
	}
	if msg == nil {
		return errors.New("nil protobuf message")
	}
	payload, err := proto.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal frame: %w", err)
	}
	if len(payload) > MaxFrameBytes {
		return fmt.Errorf("frame size %d exceeds maximum %d bytes", len(payload), MaxFrameBytes)
	}
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(payload)))
	if _, err := w.Write(lenBuf[:]); err != nil {
		return fmt.Errorf("write frame header: %w", err)
	}
	if len(payload) > 0 {
		if _, err := w.Write(payload); err != nil {
			return fmt.Errorf("write frame payload: %w", err)
		}
	}
	return nil
}

// ReadFrame reads a 4-byte big-endian length-prefixed Protobuf message from r into msg.
func ReadFrame(r io.Reader, msg proto.Message) error {
	if r == nil {
		return errors.New("nil reader")
	}
	if msg == nil {
		return errors.New("nil protobuf message")
	}
	var lenBuf [4]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return err
	}
	size := binary.BigEndian.Uint32(lenBuf[:])
	if size > MaxFrameBytes {
		return fmt.Errorf("frame size %d exceeds maximum %d bytes", size, MaxFrameBytes)
	}
	payload := make([]byte, size)
	if size > 0 {
		if _, err := io.ReadFull(r, payload); err != nil {
			return fmt.Errorf("read frame payload: %w", err)
		}
	}
	if err := proto.Unmarshal(payload, msg); err != nil {
		return fmt.Errorf("unmarshal frame: %w", err)
	}
	return nil
}

// WritePreamble writes the pre-Protobuf TCRP handshake header:
// ['T','C','R','P'][uint16 BE major][uint16 BE min_host_minor][uint32 BE token_len][token_bytes].
func WritePreamble(w io.Writer, major, minHostMinor uint16, token string) error {
	if w == nil {
		return errors.New("nil writer")
	}
	tokenBytes := []byte(token)
	if len(tokenBytes) > 4096 {
		return fmt.Errorf("preamble token too long: %d bytes", len(tokenBytes))
	}
	buf := make([]byte, 12+len(tokenBytes))
	copy(buf[0:4], PreambleMagic[:])
	binary.BigEndian.PutUint16(buf[4:6], major)
	binary.BigEndian.PutUint16(buf[6:8], minHostMinor)
	binary.BigEndian.PutUint32(buf[8:12], uint32(len(tokenBytes)))
	copy(buf[12:], tokenBytes)
	if _, err := w.Write(buf); err != nil {
		return fmt.Errorf("write preamble: %w", err)
	}
	return nil
}

// ReadPreamble reads and validates the pre-Protobuf TCRP handshake header.
func ReadPreamble(r io.Reader) (major, minHostMinor uint16, token string, err error) {
	if r == nil {
		return 0, 0, "", errors.New("nil reader")
	}
	var hdr [12]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return 0, 0, "", fmt.Errorf("read preamble header: %w", err)
	}
	if [4]byte(hdr[0:4]) != PreambleMagic {
		return 0, 0, "", fmt.Errorf("invalid preamble magic %q (expected TCRP)", string(hdr[0:4]))
	}
	major = binary.BigEndian.Uint16(hdr[4:6])
	minHostMinor = binary.BigEndian.Uint16(hdr[6:8])
	tokenLen := binary.BigEndian.Uint32(hdr[8:12])
	if tokenLen > 4096 {
		return 0, 0, "", fmt.Errorf("preamble token too long: %d bytes", tokenLen)
	}
	tokenBytes := make([]byte, tokenLen)
	if tokenLen > 0 {
		if _, err := io.ReadFull(r, tokenBytes); err != nil {
			return 0, 0, "", fmt.Errorf("read preamble token: %w", err)
		}
	}
	return major, minHostMinor, string(tokenBytes), nil
}
