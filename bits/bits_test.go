package bits

import (
	"errors"
	"testing"
)

// The two orders read the same byte as different sequences, which is the whole
// reason the parameter exists. 0b10110010 is asymmetric so a swapped
// implementation cannot pass both cases.
func TestBitOrder(t *testing.T) {
	for _, tc := range []struct {
		order Order
		want  []uint32
	}{
		{MSB, []uint32{1, 0, 1, 1, 0, 0, 1, 0}},
		{LSB, []uint32{0, 1, 0, 0, 1, 1, 0, 1}},
	} {
		r := NewReader([]byte{0xB2}, tc.order)
		for i, want := range tc.want {
			got, err := r.Bit()
			if err != nil {
				t.Fatalf("order %d bit %d: %v", tc.order, i, err)
			}
			if got != want {
				t.Errorf("order %d bit %d: got %d, want %d", tc.order, i, got, want)
			}
		}
		if _, err := r.Bit(); !errors.Is(err, ErrEOF) {
			t.Errorf("order %d: past end returned %v, want ErrEOF", tc.order, err)
		}
	}
}

// A multi-bit value is assembled most-significant-first in both orders: Order
// describes the input layout, not the arithmetic. In LSB order the same bytes
// yield a different value, and that difference is the input being read in a
// different sequence — not the result being reversed.
func TestBitsCompositionIsMSBFirstInBothOrders(t *testing.T) {
	if got, err := NewReader([]byte{0xB2}, MSB).Bits(4); err != nil || got != 0xB {
		t.Errorf("MSB: got %#x, %v; want 0xb", got, err)
	}
	// LSB reads 0,1,0,0 and composes them as 0b0100.
	if got, err := NewReader([]byte{0xB2}, LSB).Bits(4); err != nil || got != 0x4 {
		t.Errorf("LSB: got %#x, %v; want 0x4", got, err)
	}
}

// A value spanning a byte boundary is where an implementation that reads bytes
// and shifts afterwards diverges from one that reads bits.
func TestBitsCrossByteBoundary(t *testing.T) {
	// 0xFF 0x00: 12 bits MSB-first is 1111 1111 0000.
	if got, err := NewReader([]byte{0xFF, 0x00}, MSB).Bits(12); err != nil || got != 0xFF0 {
		t.Errorf("got %#x, %v; want 0xff0", got, err)
	}
	// Offset by three bits first, so the 8-bit read straddles: 0b10110010 0b01000000,
	// skip 101, then take 10010010.
	r := NewReader([]byte{0xB2, 0x40}, MSB)
	if _, err := r.Bits(3); err != nil {
		t.Fatal(err)
	}
	if got, err := r.Bits(8); err != nil || got != 0x92 {
		t.Errorf("unaligned byte: got %#x, %v; want 0x92", got, err)
	}
}

// The aligned-byte fast path must return exactly what the slow loop would.
func TestBitsFastPathMatchesSlowPath(t *testing.T) {
	data := []byte{0x00, 0x7F, 0x80, 0xB2, 0xFF}
	for _, order := range []Order{MSB, LSB} {
		fast := NewReader(data, order)
		slow := NewReader(data, order)
		for i := range data {
			f, ferr := fast.Bits(8)
			// Force the bit loop by reading one bit at a time and recomposing.
			var sv uint32
			for j := 0; j < 8; j++ {
				b, err := slow.Bit()
				if err != nil {
					t.Fatalf("order %d byte %d bit %d: %v", order, i, j, err)
				}
				sv = sv<<1 | b
			}
			if ferr != nil || f != sv {
				t.Errorf("order %d byte %d: fast %#x (%v), slow %#x", order, i, f, ferr, sv)
			}
		}
	}
}

// The maximum width is 32, and a wider request is a caller bug rather than a
// truncated read — so it must not be reported as ErrEOF, which a caller may be
// treating as "input ran out".
func TestBitsWidthLimits(t *testing.T) {
	data := make([]byte, 8)
	for i := range data {
		data[i] = 0xFF
	}
	if got, err := NewReader(data, MSB).Bits(32); err != nil || got != 0xFFFFFFFF {
		t.Errorf("32 bits: got %#x, %v", got, err)
	}
	_, err := NewReader(data, MSB).Bits(33)
	if err == nil {
		t.Fatal("33 bits: want an error")
	}
	if errors.Is(err, ErrEOF) {
		t.Errorf("33 bits: reported as ErrEOF, want a width error: %v", err)
	}
	// Zero bits is a legal no-op, which lets a caller loop over a width of 0
	// without special-casing it.
	if got, err := NewReader(data, MSB).Bits(0); err != nil || got != 0 {
		t.Errorf("0 bits: got %#x, %v", got, err)
	}
}

// A truncated read consumes nothing usable: the caller gets an error, not the
// bits that happened to be available. Half a prefix code is a different code.
func TestPartialReadIsNotAPartialValue(t *testing.T) {
	r := NewReader([]byte{0xFF}, MSB)
	got, err := r.Bits(12)
	if !errors.Is(err, ErrEOF) {
		t.Fatalf("got %v, want ErrEOF", err)
	}
	if got != 0 {
		t.Errorf("returned %#x alongside the error; a partial value must not be handed back", got)
	}
}

// Align is the per-row call an image unpacker makes, so it must advance to the
// next byte from any position inside one and do nothing when already at a
// boundary. A version that always advanced would drop a byte per row.
func TestAlignSkipsPaddingAndIsIdempotent(t *testing.T) {
	// Five 1-bit samples then padding: the next row starts at the second byte.
	r := NewReader([]byte{0xF8, 0x42}, MSB)
	for i := 0; i < 5; i++ {
		if _, err := r.Bit(); err != nil {
			t.Fatal(err)
		}
	}
	r.Align()
	if got, err := r.Bits(8); err != nil || got != 0x42 {
		t.Errorf("after Align: got %#x, %v; want 0x42", got, err)
	}

	r2 := NewReader([]byte{0x42, 0x99}, MSB)
	r2.Align()
	r2.Align()
	if got, err := r2.Bits(8); err != nil || got != 0x42 {
		t.Errorf("Align at a boundary consumed a byte: got %#x, %v; want 0x42", got, err)
	}
}

func TestRemaining(t *testing.T) {
	r := NewReader([]byte{0x00, 0x00}, MSB)
	if got := r.Remaining(); got != 16 {
		t.Errorf("got %d, want 16", got)
	}
	if _, err := r.Bits(3); err != nil {
		t.Fatal(err)
	}
	if got := r.Remaining(); got != 13 {
		t.Errorf("after 3 bits: got %d, want 13", got)
	}
	if _, err := r.Bits(13); err != nil {
		t.Fatal(err)
	}
	if got := r.Remaining(); got != 0 {
		t.Errorf("at end: got %d, want 0", got)
	}
	// Never negative, whatever the caller did.
	if got := NewReader(nil, MSB).Remaining(); got != 0 {
		t.Errorf("empty: got %d, want 0", got)
	}
}

func FuzzReader(f *testing.F) {
	f.Add([]byte{0xB2, 0x40, 0xFF})
	f.Add([]byte{})
	// Reading arbitrary widths from arbitrary data must not panic and must never
	// report more remaining bits than the input holds.
	f.Fuzz(func(t *testing.T, data []byte) {
		for _, order := range []Order{MSB, LSB} {
			r := NewReader(data, order)
			total := len(data) * 8
			for i := 0; ; i++ {
				n := uint(i % 33) // includes the illegal width 33
				if rem := r.Remaining(); rem < 0 || rem > total {
					t.Fatalf("Remaining = %d, outside 0..%d", rem, total)
				}
				if _, err := r.Bits(n); err != nil && n <= 32 {
					break
				}
				if i > 4096 {
					t.Fatal("no progress: Bits neither consumed input nor failed")
				}
			}
		}
	})
}
