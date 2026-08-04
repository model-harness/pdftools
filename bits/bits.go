// Package bits reads a bit stream MSB-first or LSB-first.
//
// PDF has two kinds of packed data that a byte reader cannot address. Image
// samples below 8 bits per component are packed several to a byte, so a 1-bit
// bilevel image stores eight pixels in one byte and a 4-bit one stores two —
// and a row's last byte is padded, because every row starts on a byte boundary
// (ISO 32000-2 §8.9.5.1). The bit-stream codecs, CCITT G3/G4 and JBIG2, are
// built on variable-length prefix codes that straddle byte boundaries by design.
//
// Both orders exist because both occur: image samples and CCITT's default are
// MSB-first, while CCITT's /BlackIs1-adjacent LSB variant and some JBIG2
// contexts are not. Making the order a parameter rather than shipping two
// readers keeps the callers identical.
//
// This package is stdlib-only and knows nothing about PDF. It sits below filter
// (docs/DESIGN.md §4) so that the codecs above it share one implementation of
// the thing they all get wrong independently.
package bits

import (
	"errors"
	"fmt"
)

// ErrEOF reports a read past the end of the data.
//
// Distinct from io.EOF because this is not a stream: the caller knows how many
// bits it expects, and running out means the input is truncated. Returning
// io.EOF would invite callers to treat it as normal termination, which for a
// half-read prefix code it is not.
var ErrEOF = errors.New("bits: end of data")

// Order is the direction bits are consumed within each byte.
type Order int

const (
	// MSB takes bit 7 of each byte first. This is PDF's image-sample packing and
	// CCITT's usual order.
	MSB Order = iota

	// LSB takes bit 0 of each byte first.
	LSB
)

// Reader reads bits from a byte slice.
//
// The zero value is not usable; use NewReader. The slice is borrowed, not
// copied: image data is routinely tens of megabytes and the caller already
// holds it.
type Reader struct {
	data  []byte
	order Order
	// byt is the index of the byte being read, bit the number of bits already
	// taken from it. Keeping the position as a pair rather than a single bit
	// count avoids a division on every read and makes Align a two-line
	// assignment.
	byt int
	bit uint
}

// NewReader returns a Reader over data.
func NewReader(data []byte, order Order) *Reader {
	return &Reader{data: data, order: order}
}

// Bit returns the next bit as 0 or 1.
func (r *Reader) Bit() (uint32, error) {
	if r.byt >= len(r.data) {
		return 0, ErrEOF
	}
	b := r.data[r.byt]
	var v uint32
	if r.order == MSB {
		v = uint32(b>>(7-r.bit)) & 1
	} else {
		v = uint32(b>>r.bit) & 1
	}
	r.bit++
	if r.bit == 8 {
		r.bit = 0
		r.byt++
	}
	return v, nil
}

// Bits returns the next n bits, the first bit read being the most significant of
// the result. n must be 0 to 32.
//
// The result is assembled most-significant-first regardless of Order, because
// Order describes how bits are laid out in the input, not how a multi-bit value
// is composed. A 4-bit image sample read LSB-first is still the number the
// producer wrote; reversing it here would make every caller reverse it back.
func (r *Reader) Bits(n uint) (uint32, error) {
	if n > 32 {
		return 0, fmt.Errorf("bits: %d bits requested, maximum is 32", n)
	}
	// The fast path is a whole aligned byte, which is what an 8-bit sample —
	// 235 of the 239 images in the corpus — asks for on every read.
	//
	// MSB only. In LSB order the byte's bits arrive in the opposite sequence, so
	// composing them most-significant-first reverses the byte: 0x7F reads as 0xFE,
	// not 0x7F. Returning the byte verbatim there would be correct-looking and
	// wrong, which is why the fast and slow paths are pinned against each other by
	// a test rather than assumed to agree.
	if n == 8 && r.bit == 0 && r.order == MSB {
		if r.byt >= len(r.data) {
			return 0, ErrEOF
		}
		v := uint32(r.data[r.byt])
		r.byt++
		return v, nil
	}
	var v uint32
	for i := uint(0); i < n; i++ {
		b, err := r.Bit()
		if err != nil {
			// The bits already read are discarded. A partial value is not a
			// partial answer — half a prefix code or half a sample is a
			// different number, not an approximate one.
			return 0, err
		}
		v = v<<1 | b
	}
	return v, nil
}

// Align discards the rest of the current byte.
//
// This is the per-row call an image unpacker makes: a row of samples occupies a
// whole number of bytes with the remainder padded, so a 5-pixel 1-bit row is one
// byte and the next row starts at the next. Without it every row after the first
// would be shifted by the padding, which produces a recognizable but progressively
// skewed image — a failure that looks like a decoder bug rather than an alignment
// one.
//
// A no-op when already aligned, so a caller can invoke it per row without
// checking.
func (r *Reader) Align() {
	if r.bit != 0 {
		r.bit = 0
		r.byt++
	}
}

// Remaining returns the number of bits not yet read.
func (r *Reader) Remaining() int {
	if r.byt >= len(r.data) {
		return 0
	}
	return (len(r.data)-r.byt)*8 - int(r.bit)
}
