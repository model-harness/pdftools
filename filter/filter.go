// Package filter decodes PDF stream filters (ISO 32000-2 §7.4).
//
// The five filters here are the ones needed to read text: Flate, LZW,
// ASCIIHex, ASCII85, and RunLength. The image filters — DCTDecode, CCITTFaxDecode,
// JBIG2Decode, JPXDecode — are deliberately absent. Image data is passed through
// still encoded so it can be written out in its original form without a lossy
// re-encode, which is what an image extractor wants.
//
// That passthrough is the contract the image package is built on: it reads the
// codec off the chain this package stopped at, so a Flate-then-DCT stream reaches
// it as a decompressed JPEG that needs no further decoding to become a .jpg.
//
// Decoding is owned rather than borrowed because it is small, entirely covered by
// the standard library, and sits on the hot path for every page.
package filter

import (
	"bytes"
	"compress/zlib"
	"errors"
	"fmt"
	"io"

	"github.com/model-harness/pdftools/objects"
)

// ErrUnsupported is returned for a filter this package does not decode,
// including the image filters it deliberately leaves encoded.
var ErrUnsupported = errors.New("filter: unsupported")

// maxDecoded caps a single stream's decoded size at 512 MiB.
//
// Streams are untrusted, and both Flate and LZW can expand enormously from a
// small input: a few kilobytes of zeros inflates to gigabytes. Without a cap a
// crafted PDF is a memory-exhaustion denial of service against any process that
// opens it.
const maxDecoded = 512 << 20

// IsImage reports whether name is an image filter whose data should be left
// encoded. Callers use this to distinguish "cannot decode" from "must not".
func IsImage(name objects.Name) bool {
	switch name {
	case "DCTDecode", "DCT", "CCITTFaxDecode", "CCF", "JBIG2Decode", "JPXDecode":
		return true
	}
	return false
}

// Decode applies one filter to data. params may be nil.
func Decode(name objects.Name, data []byte, params objects.Dict, s objects.Store) ([]byte, error) {
	var out []byte
	var err error

	switch name {
	case "FlateDecode", "Fl":
		out, err = flateDecode(data)
	case "LZWDecode", "LZW":
		early := 1
		if params != nil {
			if v, ok := objects.GetInt(s, params, "EarlyChange"); ok {
				early = int(v)
			}
		}
		out, err = lzwDecode(data, early == 1)
	case "ASCIIHexDecode", "AHx":
		out, err = asciiHexDecode(data)
	case "ASCII85Decode", "A85":
		out, err = ascii85Decode(data)
	case "RunLengthDecode", "RL":
		out, err = runLengthDecode(data)
	case "Crypt":
		// The identity crypt filter is a no-op. Any other value means the stream
		// is encrypted, which is handled at the document level, not here.
		return data, nil
	default:
		if IsImage(name) {
			return data, fmt.Errorf("%w: %s is an image filter, left encoded", ErrUnsupported, name)
		}
		return data, fmt.Errorf("%w: %s", ErrUnsupported, name)
	}

	if err != nil {
		// Partial output is kept on error. A truncated Flate stream is common in
		// damaged files, and the bytes recovered before the break are usually the
		// whole page; discarding them loses real text for no benefit.
		return out, err
	}
	return applyPredictor(out, params, s)
}

// DecodeChain applies a stream's whole filter chain in order.
//
// A chain stops at the first image filter, returning the data as it stands with
// the remaining filters reported. That is not a failure: it is how a Flate-then-DCT
// stream yields a decompressed JPEG.
func DecodeChain(st *objects.Stream, s objects.Store) ([]byte, error) {
	data := st.Raw
	parms := decodeParms(st, s)

	for i, name := range st.Filters {
		if IsImage(name) {
			return data, fmt.Errorf("%w: chain stops at %s", ErrUnsupported, name)
		}
		var p objects.Dict
		if i < len(parms) {
			p = parms[i]
		}
		out, err := Decode(name, data, p, s)
		if err != nil {
			return out, err
		}
		data = out
	}
	return data, nil
}

// decodeParms normalizes /DecodeParms, which may be a single dictionary, an
// array of dictionaries and nulls, or absent, and is aligned positionally with
// /Filter.
func decodeParms(st *objects.Stream, s objects.Store) []objects.Dict {
	raw, ok := st.Dict["DecodeParms"]
	if !ok {
		raw, ok = st.Dict["DP"]
		if !ok {
			return nil
		}
	}
	resolved, err := s.Resolve(raw)
	if err != nil {
		return nil
	}
	arr := objects.ArrayOrSingle(resolved)
	out := make([]objects.Dict, len(arr))
	for i, item := range arr {
		v, err := s.Resolve(item)
		if err != nil {
			continue
		}
		if d, isDict := v.(objects.Dict); isDict {
			out[i] = d
		}
	}
	return out
}

// flateDecode decodes a zlib or raw-deflate stream.
func flateDecode(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, nil
	}
	zr, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		// Producers emit raw deflate without the zlib header, and others prepend
		// stray whitespace before a valid one. Try both before giving up.
		if out, rawErr := rawFlate(data); rawErr == nil {
			return out, nil
		}
		if i := firstNonSpace(data); i > 0 && i < len(data) {
			if zr2, err2 := zlib.NewReader(bytes.NewReader(data[i:])); err2 == nil {
				defer zr2.Close()
				return readCapped(zr2)
			}
		}
		return nil, fmt.Errorf("filter: flate: %w", err)
	}
	defer zr.Close()
	return readCapped(zr)
}

func firstNonSpace(b []byte) int {
	for i, c := range b {
		if c != ' ' && c != '\n' && c != '\r' && c != '\t' && c != 0 {
			return i
		}
	}
	return len(b)
}

// readCapped reads r up to maxDecoded bytes.
//
// A truncated stream returns what was read along with the error, because the
// recovered prefix is usually the entire page and is strictly better than
// nothing.
func readCapped(r io.Reader) ([]byte, error) {
	var buf bytes.Buffer
	n, err := io.Copy(&buf, io.LimitReader(r, maxDecoded))
	if err != nil {
		return buf.Bytes(), fmt.Errorf("filter: decode: %w", err)
	}
	if n == maxDecoded {
		return buf.Bytes(), fmt.Errorf("filter: decoded stream exceeds %d bytes", maxDecoded)
	}
	return buf.Bytes(), nil
}

// asciiHexDecode decodes hexadecimal, terminated by '>' (§7.4.2).
func asciiHexDecode(data []byte) ([]byte, error) {
	out := make([]byte, 0, len(data)/2)
	var cur byte
	half := false
	for _, c := range data {
		if c == '>' {
			break
		}
		v, ok := hexVal(c)
		if !ok {
			continue // whitespace and junk alike
		}
		if half {
			out = append(out, cur<<4|v)
			half = false
		} else {
			cur, half = v, true
		}
	}
	if half {
		// An odd final digit is padded with zero.
		out = append(out, cur<<4)
	}
	return out, nil
}

func hexVal(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

// ascii85Decode decodes base-85, terminated by "~>" (§7.4.3).
func ascii85Decode(data []byte) ([]byte, error) {
	out := make([]byte, 0, len(data)*4/5)
	var group [5]byte
	n := 0

	// A leading "<~" is not part of the standard but appears in the wild.
	if len(data) >= 2 && data[0] == '<' && data[1] == '~' {
		data = data[2:]
	}

	for i := 0; i < len(data); i++ {
		c := data[i]
		switch {
		case c == '~':
			goto flush
		case c == 'z' && n == 0:
			// 'z' abbreviates four zero bytes, and is only legal at a group start.
			out = append(out, 0, 0, 0, 0)
			continue
		case c < '!' || c > 'u':
			continue // whitespace and invalid bytes
		}
		group[n] = c - '!'
		n++
		if n == 5 {
			var err error
			out, err = appendGroup85(out, group, 5)
			if err != nil {
				return out, err
			}
			n = 0
		}
	}

flush:
	if n > 0 {
		if n == 1 {
			// A single leftover character encodes nothing and is malformed.
			return out, nil
		}
		// Pad the partial group with the maximum digit, then keep n-1 bytes.
		for i := n; i < 5; i++ {
			group[i] = 84
		}
		return appendGroup85(out, group, n)
	}
	return out, nil
}

// appendGroup85 decodes one base-85 group and appends its first n-1 bytes.
//
// A group encodes a 32-bit value, so five digits can describe a number the
// encoding cannot represent: "uuuuu" is 4,437,053,124. Accumulating that in a
// uint32 wraps it to 142,085,828 and emits four plausible bytes that are simply
// wrong, with no indication anything happened — the silent-corruption failure
// this package exists to avoid. The value is accumulated in uint64 and rejected
// instead.
//
// Overflow always means malformed input, so there is no case to salvage. A legal
// partial group cannot overflow after padding: the largest one-, two-, and
// three-byte prefixes pad to 4278608874, 4294908474, and 4294967124, all inside
// the 32-bit range.
func appendGroup85(out []byte, g [5]byte, n int) ([]byte, error) {
	v := uint64(0)
	for i := 0; i < 5; i++ {
		v = v*85 + uint64(g[i])
	}
	if v > 0xFFFFFFFF {
		return out, fmt.Errorf("filter: ASCII85: group value %d exceeds 32 bits", v)
	}
	b := [4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)}
	return append(out, b[:n-1]...), nil
}

// runLengthDecode decodes the byte-oriented run-length encoding of §7.4.5: a length
// byte under 128 means that many literal bytes follow, over 128 means the next
// byte repeats 257-length times, and 128 ends the data.
func runLengthDecode(data []byte) ([]byte, error) {
	out := make([]byte, 0, len(data)*2)
	for i := 0; i < len(data); {
		n := data[i]
		i++
		switch {
		case n == 128:
			return out, nil
		case n < 128:
			end := i + int(n) + 1
			if end > len(data) {
				end = len(data)
			}
			out = append(out, data[i:end]...)
			i = end
		default:
			if i >= len(data) {
				return out, nil
			}
			cnt := 257 - int(n)
			for j := 0; j < cnt; j++ {
				out = append(out, data[i])
			}
			i++
		}
		if len(out) > maxDecoded {
			return out, fmt.Errorf("filter: runlength: exceeds %d bytes", maxDecoded)
		}
	}
	// Missing the 128 terminator is malformed but the data is already recovered.
	return out, nil
}
