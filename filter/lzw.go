package filter

import (
	"bytes"
	"compress/flate"
	"fmt"
)

// rawFlate decodes headerless deflate data.
//
// A stream whose /Filter is FlateDecode should carry a zlib wrapper, but enough
// producers omit it that trying raw deflate on failure recovers real files.
func rawFlate(data []byte) ([]byte, error) {
	fr := flate.NewReader(bytes.NewReader(data))
	defer fr.Close()
	out, err := readCapped(fr)
	if err != nil && len(out) == 0 {
		return nil, err
	}
	return out, nil
}

// LZW constants per ISO 32000-2 §7.4.4. Codes 0-255 are literals, 256 clears the
// table, 257 ends the data, and assigned codes start at 258.
const (
	lzwClear = 256
	lzwEOD   = 257
	lzwFirst = 258
	lzwMax   = 4096
)

// lzwDecode implements the LZW variant PDF uses.
//
// The standard library's compress/lzw cannot be used here: PDF's default
// EarlyChange=1 increases the code width one code earlier than the base
// algorithm, and the standard library implements only the base behavior. Feeding
// PDF data through it yields garbage after the first 511 codes — not an error, so
// the corruption is silent, which is worse. Since the early-change difference is
// two lines in a decoder that is otherwise short, owning it is cheaper than
// working around it.
func lzwDecode(data []byte, earlyChange bool) ([]byte, error) {
	// Each table entry is a prefix code plus one byte, so a sequence is
	// reconstructed by walking backwards. This avoids storing whole strings and
	// bounds memory at 4096 entries regardless of input.
	var prefix [lzwMax]int
	var suffix [lzwMax]byte
	for i := 0; i < 256; i++ {
		prefix[i] = -1
		suffix[i] = byte(i)
	}

	next := lzwFirst
	width := 9
	prev := -1

	out := make([]byte, 0, len(data)*3)
	// seq is scratch for reversing a decoded sequence, reused across codes.
	seq := make([]byte, 0, 64)

	br := bitReader{data: data}
	for {
		code, ok := br.read(width)
		if !ok {
			// Running out of bits without an EOD marker is malformed, but the
			// output so far is valid.
			return out, nil
		}

		switch code {
		case lzwEOD:
			return out, nil
		case lzwClear:
			next = lzwFirst
			width = 9
			prev = -1
			continue
		}

		// Resolve the code to bytes. A code equal to next is the legal
		// "KwKwK" case, where the entry being defined is used immediately.
		seq = seq[:0]
		cur := code
		if code == next && prev >= 0 {
			seq = append(seq, firstByte(&prefix, &suffix, prev))
			cur = prev
		} else if code >= next && code >= lzwFirst {
			// A code past the end of the table is unrecoverable: the dictionary
			// state is lost, so stop and keep what was decoded.
			return out, fmt.Errorf("filter: lzw: code %d beyond table (next %d)", code, next)
		}

		for cur >= 0 && len(seq) < lzwMax {
			seq = append(seq, suffix[cur])
			cur = prefix[cur]
		}
		// seq is reversed.
		for i, j := 0, len(seq)-1; i < j; i, j = i+1, j-1 {
			seq[i], seq[j] = seq[j], seq[i]
		}
		out = append(out, seq...)
		if len(out) > maxDecoded {
			return out, fmt.Errorf("filter: lzw: exceeds %d bytes", maxDecoded)
		}

		// Add the new entry: the previous sequence plus this one's first byte.
		if prev >= 0 && next < lzwMax {
			prefix[next] = prev
			suffix[next] = seq[0]
			next++
		}
		prev = code

		// Widen the code as the table fills.
		//
		// Two separate offsets stack here, and conflating them is how this goes
		// wrong. The first is unconditional: a decoder cannot define a table entry
		// until it has read one code past the one that produced it, so it always
		// trails the encoder by exactly one entry. Comparing next directly against
		// the encoder's threshold therefore widens one code late, and one code read
		// at the wrong width desynchronizes the whole remaining stream.
		//
		// The second offset is early change itself, PDF's default, which asks the
		// encoder to widen one code before the table strictly requires it. It is an
		// additional +1 on top of the lag, not a substitute for it.
		limit := next + 1
		if earlyChange {
			limit++
		}
		switch {
		case limit > 2048 && width < 12:
			width = 12
		case limit > 1024 && width < 11:
			width = 11
		case limit > 512 && width < 10:
			width = 10
		}
	}
}

// firstByte returns the first byte of the sequence a code expands to, by walking
// its prefix chain to the root.
func firstByte(prefix *[lzwMax]int, suffix *[lzwMax]byte, code int) byte {
	for i := 0; i < lzwMax; i++ {
		if prefix[code] < 0 {
			return suffix[code]
		}
		code = prefix[code]
	}
	return suffix[code]
}

// bitReader reads big-endian bit fields, which is the order LZW and the image
// predictors both use.
type bitReader struct {
	data []byte
	pos  int
	bit  uint
}

// read returns the next n bits, or false when fewer than n remain.
func (b *bitReader) read(n int) (int, bool) {
	v := 0
	for i := 0; i < n; i++ {
		if b.pos >= len(b.data) {
			return 0, false
		}
		bit := (b.data[b.pos] >> (7 - b.bit)) & 1
		v = v<<1 | int(bit)
		b.bit++
		if b.bit == 8 {
			b.bit = 0
			b.pos++
		}
	}
	return v, true
}
