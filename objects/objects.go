// Package objects defines the PDF object model this repository owns, plus the
// Store interface for reading it out of a file.
//
// The object model is deliberately re-declared here rather than borrowed from
// whichever library currently parses the file. That library is an
// implementation detail behind Store; if it is replaced, adapters change and
// callers do not. The cost is one translation layer in each adapter, paid once.
//
// The model follows ISO 32000-2 §7.3: eight basic object types, plus streams
// and indirect references.
package objects

import (
	"errors"
	"fmt"
	"io"
)

// ErrNotFound is returned when a referenced object is absent. A malformed PDF
// with dangling references is common enough that this is an expected outcome,
// not an exceptional one.
var ErrNotFound = errors.New("objects: not found")

// Object is any PDF object. The set is closed: only the types in this package
// implement it.
type Object interface{ isObject() }

// Null is the PDF null object.
type Null struct{}

// Bool is a PDF boolean.
type Bool bool

// Int is a PDF integer.
type Int int64

// Real is a PDF real number.
type Real float64

// String is a PDF string. It holds raw bytes: PDF strings are not necessarily
// text, and those that are may be PDFDocEncoded or UTF-16BE. Decoding is the
// caller's decision, not this type's.
type String []byte

// Name is a PDF name, stored without the leading slash and with #xx escapes
// already resolved.
type Name string

// Array is a PDF array.
type Array []Object

// Dict is a PDF dictionary. Keys are stored without the leading slash.
type Dict map[Name]Object

// Ref is an indirect reference.
type Ref struct {
	Num, Gen int
}

// Stream is a PDF stream: a dictionary plus data.
//
// Raw holds bytes exactly as they appear in the file, still encoded by
// Filters. Decoded holds the result of applying the filter chain, and is nil
// until a decode is attempted.
//
// Both are kept because the distinction is load-bearing. Image extraction wants
// Raw so a JPEG can be written out in its original encoding without a
// re-encode; content-stream interpretation wants Decoded.
type Stream struct {
	Dict    Dict
	Raw     []byte
	Decoded []byte

	// Filters is the filter chain in application order, as named in /Filter.
	Filters []Name
}

func (Null) isObject()    {}
func (Bool) isObject()    {}
func (Int) isObject()     {}
func (Real) isObject()    {}
func (String) isObject()  {}
func (Name) isObject()    {}
func (Array) isObject()   {}
func (Dict) isObject()    {}
func (Ref) isObject()     {}
func (*Stream) isObject() {}

func (r Ref) String() string { return fmt.Sprintf("%d %d R", r.Num, r.Gen) }

// Store gives read access to a PDF's object graph.
//
// It is declared here, by the packages that consume it, and implemented by
// adapters in subdirectories. Every method may hit the file, so an
// implementation is not required to be safe for concurrent use unless it says
// so; page-level parallelism uses one Store per worker or an explicitly
// concurrent implementation.
type Store interface {
	// Resolve follows indirect references until it reaches a direct object.
	// A non-Ref argument is returned unchanged. A dangling reference resolves
	// to Null rather than an error, matching the PDF rule that a reference to a
	// nonexistent object is null.
	Resolve(Object) (Object, error)

	// Trailer returns the trailer dictionary.
	Trailer() (Dict, error)

	// Catalog returns the document catalog, /Root.
	Catalog() (Dict, error)

	// PageCount returns the number of pages.
	PageCount() int

	// Page returns the page dictionary for a 1-based page number, with
	// inheritable attributes (/Resources, /MediaBox, /CropBox, /Rotate) already
	// resolved from ancestors per ISO 32000-2 §7.7.3.4.
	Page(n int) (Dict, error)

	// PageContent returns the concatenated, decoded content streams of a 1-based
	// page. A page's /Contents may be an array of streams that must be joined
	// with intervening whitespace before tokenizing, which this handles.
	PageContent(n int) ([]byte, error)

	// Decode applies a stream's filter chain, populating its Decoded field.
	//
	// Required on the interface rather than left to the adapter because most
	// streams worth reading are not page content: a /ToUnicode CMap, an embedded
	// CMap, a font program, an embedded file. Resolve returns those with Decoded
	// nil, since decoding every stream a caller merely looked at would decompress
	// the whole document.
	//
	// A stream whose final filter is an image codec is left alone and reports no
	// error: its output is pixels rather than bytes, and callers that want the
	// original JPEG or CCITT data want Raw. Decoded staying nil is the signal to
	// use Raw.
	Decode(*Stream) error

	// Version reports the PDF version, preferring the catalog's /Version over
	// the header when both are present, per ISO 32000-2 §7.5.5.
	Version() string

	// Encrypted reports whether the file has an /Encrypt dictionary. A file may
	// be readable and still report true: empty-password encryption is common.
	Encrypted() bool

	// Close releases any resources held.
	io.Closer
}

// Getters below tolerate a nil or wrong-typed value and report failure through
// their second return, because reading a real PDF means constantly asking for
// keys that may be absent or the wrong type. Returning an error for each would
// bury the actual logic in error handling for a condition that is routine.

// Get resolves d[key].
func Get(s Store, d Dict, key Name) (Object, bool) {
	v, ok := d[key]
	if !ok {
		return nil, false
	}
	r, err := s.Resolve(v)
	if err != nil {
		return nil, false
	}
	if _, isNull := r.(Null); isNull {
		return nil, false
	}
	return r, true
}

// GetDict resolves d[key] as a dictionary. A stream also satisfies this, since
// a stream is a dictionary with data attached and callers routinely need the
// dictionary half.
func GetDict(s Store, d Dict, key Name) (Dict, bool) {
	v, ok := Get(s, d, key)
	if !ok {
		return nil, false
	}
	switch t := v.(type) {
	case Dict:
		return t, true
	case *Stream:
		return t.Dict, true
	}
	return nil, false
}

// GetArray resolves d[key] as an array.
func GetArray(s Store, d Dict, key Name) (Array, bool) {
	v, ok := Get(s, d, key)
	if !ok {
		return nil, false
	}
	a, ok := v.(Array)
	return a, ok
}

// GetName resolves d[key] as a name.
func GetName(s Store, d Dict, key Name) (Name, bool) {
	v, ok := Get(s, d, key)
	if !ok {
		return "", false
	}
	n, ok := v.(Name)
	return n, ok
}

// GetStream resolves d[key] as a stream.
//
// The stream's Decoded field is nil unless something already decoded it. Use
// GetStreamData when the bytes are what you want.
func GetStream(s Store, d Dict, key Name) (*Stream, bool) {
	v, ok := Get(s, d, key)
	if !ok {
		return nil, false
	}
	st, ok := v.(*Stream)
	return st, ok
}

// GetStreamData resolves d[key] as a stream and returns its decoded bytes.
//
// This is the form nearly every caller wants, and it exists because the
// two-step version has a silent failure mode: a stream fetched with GetStream
// has Decoded nil until someone calls Decode, so reading it directly yields
// nothing and looks like an empty stream rather than an un-decoded one.
//
// A decode failure reports false rather than an error, matching the other
// getters: a stream this package cannot decode is usually an image codec, which
// is a routine outcome and not the caller's problem to distinguish here.
func GetStreamData(s Store, d Dict, key Name) ([]byte, bool) {
	st, ok := GetStream(s, d, key)
	if !ok {
		return nil, false
	}
	if st.Decoded == nil {
		if err := s.Decode(st); err != nil {
			return nil, false
		}
	}
	return st.Decoded, st.Decoded != nil
}

// GetInt resolves d[key] as an integer. A Real is accepted and truncated:
// producers write integers as reals often enough that rejecting them loses real
// data for no benefit.
func GetInt(s Store, d Dict, key Name) (int64, bool) {
	v, ok := Get(s, d, key)
	if !ok {
		return 0, false
	}
	switch t := v.(type) {
	case Int:
		return int64(t), true
	case Real:
		return int64(t), true
	}
	return 0, false
}

// GetNum resolves d[key] as a number.
func GetNum(s Store, d Dict, key Name) (float64, bool) {
	v, ok := Get(s, d, key)
	if !ok {
		return 0, false
	}
	switch t := v.(type) {
	case Int:
		return float64(t), true
	case Real:
		return float64(t), true
	}
	return 0, false
}

// GetBool resolves d[key] as a boolean.
func GetBool(s Store, d Dict, key Name) (bool, bool) {
	v, ok := Get(s, d, key)
	if !ok {
		return false, false
	}
	b, ok := v.(Bool)
	return bool(b), ok
}

// AsNum returns the numeric value of an already-resolved object.
func AsNum(o Object) (float64, bool) {
	switch t := o.(type) {
	case Int:
		return float64(t), true
	case Real:
		return float64(t), true
	}
	return 0, false
}

// DecodeTextString converts a PDF text string to a Go string per ISO 32000-2
// §7.9.2.1: UTF-16BE when the byte-order mark is present, PDFDocEncoded
// otherwise.
//
// This lives here rather than in a caller because PDF text strings appear all
// over the format -- /Lang, /Title, /ActualText, outline titles, form field
// values -- and every one of them needs the same BOM check. Skipping it yields
// text with an interleaved NUL between every character.
//
// Only the Latin range of PDFDocEncoding is handled, where it agrees with
// Unicode. Positions 0x80-0x9F differ, but text strings in practice do not use
// them.
func DecodeTextString(o Object) string {
	switch t := o.(type) {
	case String:
		return decodeBytes(t)
	case Name:
		return string(t)
	}
	return ""
}

func decodeBytes(b []byte) string {
	if len(b) >= 2 && b[0] == 0xFE && b[1] == 0xFF {
		return decodeUTF16BE(b[2:])
	}
	return string(b)
}

func decodeUTF16BE(b []byte) string {
	if len(b)%2 != 0 {
		b = b[:len(b)-1]
	}
	runes := make([]rune, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		u := rune(b[i])<<8 | rune(b[i+1])
		if u >= 0xD800 && u <= 0xDBFF && i+3 < len(b) {
			lo := rune(b[i+2])<<8 | rune(b[i+3])
			if lo >= 0xDC00 && lo <= 0xDFFF {
				runes = append(runes, 0x10000+(u-0xD800)<<10+(lo-0xDC00))
				i += 2
				continue
			}
		}
		runes = append(runes, u)
	}
	return string(runes)
}

// ArrayOrSingle normalizes a value that the specification allows to be either a
// single object or an array of them, which PDF does in many places
// (/Contents, /Filter, /Annots).
func ArrayOrSingle(o Object) Array {
	switch t := o.(type) {
	case nil:
		return nil
	case Array:
		return t
	case Null:
		return nil
	}
	return Array{o}
}
