// Package pdfcpu adapts github.com/pdfcpu/pdfcpu (Apache-2.0, pure Go) to the
// objects.Store interface.
//
// This is a borrowed layer, isolated so it can be replaced by a native parser
// without touching callers. Everything pdfcpu-specific stays inside this file:
// its types never appear in a signature that objects.Store exposes.
package pdfcpu

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"

	pcapi "github.com/pdfcpu/pdfcpu/pkg/api"
	pcfilter "github.com/pdfcpu/pdfcpu/pkg/filter"
	pcmodel "github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	pctypes "github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"

	"github.com/model-harness/pdftools/objects"
)

// store implements objects.Store over a pdfcpu context.
type store struct {
	ctx    *pcmodel.Context
	closer io.Closer
}

var _ objects.Store = (*store)(nil)

// Open reads the PDF at path.
//
// The path comes from the caller by design — this is a library and CLI whose
// purpose is to open the file a user names. Callers that need to confine reads to
// a directory should resolve and check the path themselves, then use New with an
// *os.File, or use os.Root and pass the resulting file.
func Open(path string) (objects.Store, error) {
	f, err := os.Open(path) // #nosec G304 -- opening a caller-named file is the API
	if err != nil {
		return nil, err
	}
	s, err := New(f)
	if err != nil {
		// The parse already failed; a close error here would only mask it.
		_ = f.Close()
		return nil, err
	}
	s.(*store).closer = f
	return s, nil
}

// New reads a PDF from rs. The reader must stay valid for the lifetime of the
// Store: pdfcpu resolves object streams lazily and seeks back to the file.
func New(rs io.ReadSeeker) (objects.Store, error) {
	conf := pcmodel.NewDefaultConfiguration()
	// Validation relaxed on purpose. The corpora that most need extraction are
	// the ones producers got wrong, so a strict parse would reject exactly the
	// files this toolkit exists to read.
	conf.ValidationMode = pcmodel.ValidationRelaxed

	ctx, err := pcapi.ReadContext(rs, conf)
	if err != nil {
		return nil, fmt.Errorf("pdfcpu: read: %w", err)
	}
	// Page lookup needs /Count on the page tree root, which a damaged file may
	// omit. Establishing it here means Page and PageCount can stay simple.
	if err := ctx.EnsurePageCount(); err != nil {
		return nil, fmt.Errorf("pdfcpu: page count: %w", err)
	}
	return &store{ctx: ctx}, nil
}

func (s *store) Close() error {
	if s.closer != nil {
		return s.closer.Close()
	}
	return nil
}

func (s *store) PageCount() int { return s.ctx.PageCount }

func (s *store) Version() string { return s.ctx.XRefTable.VersionString() }

func (s *store) Encrypted() bool { return s.ctx.XRefTable.Encrypt != nil }

func (s *store) Resolve(o objects.Object) (objects.Object, error) {
	// Loop rather than recurse once: a reference may point at another
	// reference, which the specification permits. The bound stops a
	// self-referential chain in a damaged file from hanging.
	for i := 0; i < 32; i++ {
		ref, isRef := o.(objects.Ref)
		if !isRef {
			return o, nil
		}
		// Dereference asserts types.IndirectRef by value, so a pointer silently
		// falls through its type switch and comes back unresolved.
		pcObj, err := s.ctx.XRefTable.Dereference(*pctypes.NewIndirectRef(ref.Num, ref.Gen))
		if err != nil {
			// A reference to a missing object is null per ISO 32000-2 7.3.9, and
			// broken references are common. Treating it as an error would abort
			// extraction over a defect the specification already defines away.
			return objects.Null{}, nil
		}
		conv, err := s.conv(pcObj)
		if err != nil {
			return nil, err
		}
		if next, isRef := conv.(objects.Ref); isRef && next != ref {
			o = next
			continue
		}
		return conv, nil
	}
	return objects.Null{}, nil
}

func (s *store) Trailer() (objects.Dict, error) {
	// pdfcpu folds the trailer into the xref table rather than keeping the raw
	// dictionary, so reconstruct the entries callers actually use.
	d := objects.Dict{}
	xt := s.ctx.XRefTable
	if xt.Root != nil {
		d["Root"] = objects.Ref{Num: xt.Root.ObjectNumber.Value(), Gen: xt.Root.GenerationNumber.Value()}
	}
	if xt.Encrypt != nil {
		d["Encrypt"] = objects.Ref{Num: xt.Encrypt.ObjectNumber.Value(), Gen: xt.Encrypt.GenerationNumber.Value()}
	}
	if xt.Size != nil {
		d["Size"] = objects.Int(*xt.Size)
	}
	return d, nil
}

func (s *store) Catalog() (objects.Dict, error) {
	pcDict, err := s.ctx.XRefTable.Catalog()
	if err != nil {
		return nil, fmt.Errorf("pdfcpu: catalog: %w", err)
	}
	return s.convDict(pcDict)
}

func (s *store) Page(n int) (objects.Dict, error) {
	if n < 1 || n > s.ctx.PageCount {
		return nil, fmt.Errorf("objects: page %d out of range 1..%d: %w", n, s.ctx.PageCount, objects.ErrNotFound)
	}
	// consolidateRes=true makes pdfcpu walk the page tree and merge inherited
	// /Resources, which the Store contract requires.
	pcDict, _, attrs, err := s.ctx.XRefTable.PageDict(n, true)
	if err != nil {
		return nil, fmt.Errorf("pdfcpu: page %d: %w", n, err)
	}
	if pcDict == nil {
		return nil, fmt.Errorf("objects: page %d: %w", n, objects.ErrNotFound)
	}
	d, err := s.convDict(pcDict)
	if err != nil {
		return nil, err
	}
	// PageDict returns inherited attributes out of band; fold them in so the
	// page dictionary is self-contained, as the Store contract promises.
	if attrs != nil {
		if _, ok := d["Resources"]; !ok && attrs.Resources != nil {
			if r, err := s.convDict(attrs.Resources); err == nil {
				d["Resources"] = r
			}
		}
		if _, ok := d["MediaBox"]; !ok && attrs.MediaBox != nil {
			d["MediaBox"] = rectArray(attrs.MediaBox)
		}
		if _, ok := d["CropBox"]; !ok && attrs.CropBox != nil {
			d["CropBox"] = rectArray(attrs.CropBox)
		}
		if _, ok := d["Rotate"]; !ok && attrs.Rotate != 0 {
			d["Rotate"] = objects.Int(attrs.Rotate)
		}
	}
	return d, nil
}

func (s *store) PageContent(n int) ([]byte, error) {
	if n < 1 || n > s.ctx.PageCount {
		return nil, fmt.Errorf("objects: page %d out of range 1..%d: %w", n, s.ctx.PageCount, objects.ErrNotFound)
	}
	pcDict, _, _, err := s.ctx.XRefTable.PageDict(n, false)
	if err != nil {
		return nil, fmt.Errorf("pdfcpu: page %d: %w", n, err)
	}
	bb, err := s.ctx.XRefTable.PageContent(pcDict, n)
	if err != nil {
		// A page with no /Contents is legal and blank. pdfcpu signals this with
		// a sentinel rather than an empty slice.
		if errors.Is(err, pcmodel.ErrNoContent) {
			return nil, nil
		}
		return nil, fmt.Errorf("pdfcpu: page %d content: %w", n, err)
	}
	return bb, nil
}

func rectArray(r *pctypes.Rectangle) objects.Array {
	return objects.Array{
		objects.Real(r.LL.X), objects.Real(r.LL.Y),
		objects.Real(r.UR.X), objects.Real(r.UR.Y),
	}
}

// conv translates a pdfcpu object into this repository's model.
//
// Translation is shallow for containers: nested indirect references stay
// unresolved as objects.Ref. Resolving eagerly would pull the whole object graph
// into memory for a single lookup, which on a 1000-page document means reading
// the entire file to answer one question.
func (s *store) conv(o pctypes.Object) (objects.Object, error) {
	switch t := o.(type) {
	case nil:
		return objects.Null{}, nil
	case pctypes.Boolean:
		return objects.Bool(bool(t)), nil
	case pctypes.Integer:
		return objects.Int(t.Value()), nil
	case pctypes.Float:
		return objects.Real(t.Value()), nil
	case pctypes.Name:
		// pdfcpu keeps names with the leading slash and escapes intact; the
		// Value method strips and unescapes.
		return objects.Name(t.Value()), nil
	case pctypes.StringLiteral:
		b, err := pctypes.Unescape(t.Value())
		if err != nil {
			// Keep the raw bytes: a malformed escape should not lose the string.
			return objects.String(t.Value()), nil
		}
		return objects.String(b), nil
	case pctypes.HexLiteral:
		b, err := t.Bytes()
		if err != nil {
			return objects.String(nil), nil
		}
		return objects.String(b), nil
	case pctypes.IndirectRef:
		return objects.Ref{Num: t.ObjectNumber.Value(), Gen: t.GenerationNumber.Value()}, nil
	case *pctypes.IndirectRef:
		if t == nil {
			return objects.Null{}, nil
		}
		return objects.Ref{Num: t.ObjectNumber.Value(), Gen: t.GenerationNumber.Value()}, nil
	case pctypes.Array:
		return s.convArray(t)
	case pctypes.Dict:
		return s.convDict(t)
	case pctypes.StreamDict:
		return s.convStream(&t)
	case *pctypes.StreamDict:
		return s.convStream(t)
	case pctypes.ObjectStreamDict:
		return s.convStream(&t.StreamDict)
	case *pctypes.ObjectStreamDict:
		return s.convStream(&t.StreamDict)
	case pctypes.XRefStreamDict:
		return s.convStream(&t.StreamDict)
	case *pctypes.XRefStreamDict:
		return s.convStream(&t.StreamDict)
	}
	return nil, fmt.Errorf("pdfcpu: unhandled object type %T", o)
}

func (s *store) convArray(a pctypes.Array) (objects.Array, error) {
	out := make(objects.Array, len(a))
	for i, e := range a {
		v, err := s.conv(e)
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}

func (s *store) convDict(d pctypes.Dict) (objects.Dict, error) {
	out := make(objects.Dict, len(d))
	for k, v := range d {
		c, err := s.conv(v)
		if err != nil {
			return nil, err
		}
		out[objects.Name(k)] = c
	}
	return out, nil
}

func (s *store) convStream(sd *pctypes.StreamDict) (*objects.Stream, error) {
	d, err := s.convDict(sd.Dict)
	if err != nil {
		return nil, err
	}
	st := &objects.Stream{Dict: d, Raw: sd.Raw}
	for _, f := range sd.FilterPipeline {
		st.Filters = append(st.Filters, objects.Name(f.Name))
	}
	// Decoded data is populated only if pdfcpu already has it. Decoding here
	// would mean decompressing every stream a caller merely looked at, so the
	// decision is deferred to Decode.
	st.Decoded = sd.Content
	return st, nil
}

// Decode decodes a stream's filter chain, populating Decoded. It implements
// objects.Store.Decode; the work is the adapter's because the borrowed parser
// owns the filter implementations until the native filter package takes over.
//
// Image streams are left alone: their final filter is an image codec whose
// output is pixels, not bytes, and callers wanting the original JPEG or CCITT
// data want Raw. Decoded stays nil in that case, which is the signal to use Raw.
func (s *store) Decode(st *objects.Stream) error {
	sd := pctypes.StreamDict{
		Dict: pctypes.Dict{},
		Raw:  st.Raw,
	}
	for k, v := range st.Dict {
		p, err := s.back(v)
		if err != nil {
			return err
		}
		sd.Dict[string(k)] = p
	}
	fp, err := filterPipeline(st.Filters)
	if err != nil {
		return err
	}
	sd.FilterPipeline = fp
	if err := sd.Decode(); err != nil {
		if errors.Is(err, pcfilter.ErrUnsupportedFilter) {
			return nil // image codec; caller uses Raw
		}
		return fmt.Errorf("pdfcpu: decode stream: %w", err)
	}
	st.Decoded = sd.Content
	return nil
}

// filterPipeline translates a filter chain for pdfcpu.
//
// Nil for an empty chain, not an empty slice. pdfcpu distinguishes the two: a nil
// pipeline takes the path that returns the raw bytes unchanged, while an empty
// non-nil one reaches the decoding loop, whose body never runs, and pdfcpu then
// reads from a nil reader and panics. An unfiltered content stream — a form
// XObject written without compression — is exactly that case, and page 269 of ISO
// 32000-2 has one.
func filterPipeline(names []objects.Name) ([]pctypes.PDFFilter, error) {
	if len(names) == 0 {
		return nil, nil
	}
	out := make([]pctypes.PDFFilter, 0, len(names))
	for _, n := range names {
		out = append(out, pctypes.PDFFilter{Name: string(n)})
	}
	return out, nil
}

// back translates this repository's model into pdfcpu's, for the few operations
// that must hand objects back. Only the cases Decode needs are covered.
func (s *store) back(o objects.Object) (pctypes.Object, error) {
	switch t := o.(type) {
	case objects.Null, nil:
		return nil, nil
	case objects.Bool:
		return pctypes.Boolean(bool(t)), nil
	case objects.Int:
		return pctypes.Integer(int(t)), nil
	case objects.Real:
		return pctypes.Float(float64(t)), nil
	case objects.Name:
		return pctypes.Name(string(t)), nil
	case objects.String:
		return pctypes.StringLiteral(string(t)), nil
	case objects.Ref:
		return *pctypes.NewIndirectRef(t.Num, t.Gen), nil
	case objects.Array:
		out := make(pctypes.Array, len(t))
		for i, e := range t {
			v, err := s.back(e)
			if err != nil {
				return nil, err
			}
			out[i] = v
		}
		return out, nil
	case objects.Dict:
		out := make(pctypes.Dict, len(t))
		for k, v := range t {
			p, err := s.back(v)
			if err != nil {
				return nil, err
			}
			out[string(k)] = p
		}
		return out, nil
	}
	return nil, fmt.Errorf("pdfcpu: cannot convert %T back", o)
}

// Decoder is implemented by stores that can decode stream filter chains.
// Callers type-assert for it so the capability is discoverable without widening
// objects.Store, which a native parser will satisfy directly.
type Decoder interface {
	Decode(*objects.Stream) error
}

var _ Decoder = (*store)(nil)

// ReadStats reports what the parser observed about file structure. It is
// adapter-specific diagnostic detail, surfaced for `probe` rather than promoted
// into objects.Store, because a different parser would report different things.
type ReadStats struct {
	FileSize           int64
	Linearized         bool
	Hybrid             bool
	UsingObjectStreams bool
	UsingXRefStreams   bool
	BinaryTotalSize    int64
	BinaryImageSize    int64
	BinaryFontSize     int64
}

// Stats returns parser observations about the file.
func (s *store) Stats() ReadStats {
	rc := s.ctx.Read
	if rc == nil {
		return ReadStats{}
	}
	return ReadStats{
		FileSize:           rc.FileSize,
		Linearized:         rc.Linearized,
		Hybrid:             rc.Hybrid,
		UsingObjectStreams: rc.UsingObjectStreams,
		UsingXRefStreams:   rc.UsingXRefStreams,
		BinaryTotalSize:    rc.BinaryTotalSize,
		BinaryImageSize:    rc.BinaryImageSize,
		BinaryFontSize:     rc.BinaryFontSize,
	}
}

// Statser is implemented by stores that report file-structure statistics.
type Statser interface {
	Stats() ReadStats
}

var _ Statser = (*store)(nil)

// NewFromBytes reads a PDF held in memory.
func NewFromBytes(b []byte) (objects.Store, error) { return New(bytes.NewReader(b)) }
