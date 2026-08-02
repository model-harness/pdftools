package filter

import (
	"fmt"

	"github.com/3rg0n/pdf-spec/objects"
)

// applyPredictor reverses the predictor a Flate or LZW stream was encoded with
// (ISO 32000-2 §7.4.4.4).
//
// This is not optional in practice. Cross-reference streams and object streams
// are routinely PNG-predicted, and a predicted stream decoded without reversing
// the prediction produces bytes that look like data but are wrong — every value
// off by its neighbor. That failure is silent, which is why it belongs next to
// the decoders rather than in a caller.
func applyPredictor(data []byte, params objects.Dict, s objects.Store) ([]byte, error) {
	if params == nil || len(data) == 0 {
		return data, nil
	}
	pred, ok := objects.GetInt(s, params, "Predictor")
	if !ok || pred <= 1 {
		return data, nil
	}

	colors := intParam(s, params, "Colors", 1)
	bpc := intParam(s, params, "BitsPerComponent", 8)
	columns := intParam(s, params, "Columns", 1)

	if colors <= 0 || bpc <= 0 || columns <= 0 {
		return data, fmt.Errorf("filter: predictor: bad geometry colors=%d bpc=%d columns=%d",
			colors, bpc, columns)
	}
	// Bound each factor, then the product. Bounding only the factors is not enough
	// and was the original bug here: colors=32, bpc=32, columns=2^24 each pass
	// individually while multiplying out to a 2 GiB row, so four bytes of input
	// bought three multi-gigabyte allocations — out, prev, and cur. The product is
	// computed in int64 so the check cannot itself overflow on a 32-bit build,
	// where int is 32 bits and 32*32*2^24 wraps to zero.
	if colors > 32 || bpc > 32 || columns > 1<<24 {
		return data, fmt.Errorf("filter: predictor: implausible geometry colors=%d bpc=%d columns=%d",
			colors, bpc, columns)
	}
	rowBits := int64(colors) * int64(bpc) * int64(columns)
	rowBytes := (rowBits + 7) / 8
	// A predicted row is one scanline or one cross-reference entry set. maxDecoded
	// bounds the whole stream, so a single row at or above it is never real, and
	// the check has to happen before the value reaches make.
	if rowBytes >= maxDecoded {
		return data, fmt.Errorf("filter: predictor: row of %d bytes exceeds the %d cap (colors=%d bpc=%d columns=%d)",
			rowBytes, maxDecoded, colors, bpc, columns)
	}

	// Bytes per pixel, floored at 1: sub-byte pixels still predict against the
	// preceding byte, and a zero stride would make the Sub and Paeth loops
	// self-referential.
	bpp := (colors*bpc + 7) / 8
	if bpp < 1 {
		bpp = 1
	}
	rowLen := int(rowBytes)
	if rowLen < 1 {
		return data, nil
	}

	if pred == 2 {
		return tiffPredictor(data, colors, bpc, columns, rowLen)
	}
	return pngPredictor(data, bpp, rowLen)
}

func intParam(s objects.Store, d objects.Dict, key objects.Name, def int) int {
	if v, ok := objects.GetInt(s, d, key); ok {
		return int(v)
	}
	return def
}

// pngPredictor reverses PNG filtering, predictors 10 through 15. Each row is
// preceded by a filter-type byte, so the row stride is rowLen+1.
func pngPredictor(data []byte, bpp, rowLen int) ([]byte, error) {
	stride := rowLen + 1
	rows := len(data) / stride
	out := make([]byte, 0, rows*rowLen)

	prev := make([]byte, rowLen)
	cur := make([]byte, rowLen)

	for off := 0; off+1 <= len(data); off += stride {
		ft := data[off]
		end := off + 1 + rowLen
		if end > len(data) {
			end = len(data)
		}
		n := copy(cur, data[off+1:end])
		// A short final row is truncated data. Zero the tail so the predictor
		// operates on defined bytes rather than the previous row's leftovers.
		for i := n; i < rowLen; i++ {
			cur[i] = 0
		}

		switch ft {
		case 0: // None
		case 1: // Sub: left
			for i := bpp; i < rowLen; i++ {
				cur[i] += cur[i-bpp]
			}
		case 2: // Up: above
			for i := 0; i < rowLen; i++ {
				cur[i] += prev[i]
			}
		case 3: // Average of left and above
			for i := 0; i < rowLen; i++ {
				left := 0
				if i >= bpp {
					left = int(cur[i-bpp])
				}
				// Both terms are bytes, so their mean is at most 255 and the
				// conversion cannot truncate.
				cur[i] += byte((left + int(prev[i])) / 2) // #nosec G115 -- mean of two bytes fits
			}
		case 4: // Paeth
			for i := 0; i < rowLen; i++ {
				var left, upLeft byte
				if i >= bpp {
					left = cur[i-bpp]
					upLeft = prev[i-bpp]
				}
				cur[i] += paeth(left, prev[i], upLeft)
			}
		default:
			// An unknown filter type means the stream is not actually
			// PNG-predicted or is damaged. Returning the rows recovered so far
			// beats returning nothing.
			return out, fmt.Errorf("filter: predictor: unknown PNG filter type %d", ft)
		}

		out = append(out, cur[:rowLen]...)
		prev, cur = cur, prev
	}
	return out, nil
}

// paeth is the PNG Paeth predictor: choose whichever of left, above, or
// upper-left is closest to their linear estimate.
func paeth(a, b, c byte) byte {
	p := int(a) + int(b) - int(c)
	pa, pb, pc := abs(p-int(a)), abs(p-int(b)), abs(p-int(c))
	if pa <= pb && pa <= pc {
		return a
	}
	if pb <= pc {
		return b
	}
	return c
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// tiffPredictor reverses TIFF predictor 2: horizontal differencing, with no
// per-row filter byte. Only 8-bit components are handled; other depths require
// sub-byte arithmetic and do not appear in the streams this package decodes.
func tiffPredictor(data []byte, colors, bpc, columns, rowLen int) ([]byte, error) {
	if bpc != 8 {
		return data, fmt.Errorf("filter: predictor: TIFF predictor with %d bits per component unsupported", bpc)
	}
	out := make([]byte, len(data))
	copy(out, data)
	for row := 0; row+rowLen <= len(out); row += rowLen {
		r := out[row : row+rowLen]
		for i := colors; i < len(r); i++ {
			r[i] += r[i-colors]
		}
	}
	return out, nil
}
