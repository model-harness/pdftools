# 1. Borrow pdfcpu behind an owned object model

Date: 2026-08-02

## Status

Accepted

## Context

The repo's goal is native Go and Rust PDF libraries, but writing a conformant parser for a
1,023-page specification before delivering anything useful would mean months with no
working tool. The stated direction is explicit: start by importing libraries to handle
sub-problems, and replace them over time.

The risk in borrowing is not the dependency itself, it is that a borrowed library's types
leak into every caller. Once `pcmodel.Context` and `pctypes.IndirectRef` appear in package
signatures across the tree, replacing the parser stops being an adapter swap and becomes a
rewrite of everything that touched it. That is the failure mode that keeps half-finished
libraries half-finished.

pdfcpu v0.13.0 is the right thing to borrow: pure Go with no CGO, Apache-2.0, and — unlike
most alternatives — it exposes its full object graph publicly (`XRefTable.Dereference`,
`PageDict`, `PageContent`, `Catalog`), which is what an adapter needs.

## Decision

Declare an owned PDF object model in `objects` — `Null`, `Bool`, `Int`, `Real`, `String`,
`Name`, `Array`, `Dict`, `Ref`, `*Stream` — behind a closed interface, and an `objects.Store`
interface for document access. Every higher layer reads PDFs only through `Store`.

pdfcpu lives in exactly one package, `objects/pdfcpu`, which translates between its types
and ours at the boundary. No pdfcpu type appears anywhere else in the tree.

Two supporting rules:

- Interfaces are declared by the *consuming* package, with adapters in subpackages beneath
  it (`objects` declares `Store`, `objects/pdfcpu` implements it). No `ports/` directory —
  that is a layout borrowed from languages without structural interfaces.
- Where an adapter can do more than the interface promises, expose the extra as a separate
  capability interface (`Decoder`, `Statser`) that callers type-assert for, rather than
  widening `Store` for one consumer.

This applies to every borrowed dependency, not just pdfcpu: rasterization, OCR, and image
codecs each sit behind an owned interface.

## Consequences

Re-declaring an object model that pdfcpu already has is duplicated work, and the boundary
costs a translation on every read. That is the price of the swap staying cheap.

Translation is deliberately shallow — nested references stay unresolved rather than being
eagerly followed — so converting a container does not pull the whole object graph into
memory. Callers resolve as they descend.

The boundary earns its keep immediately rather than only at replacement time. pdfcpu's
`Dereference` type-asserts `types.IndirectRef` by value while its own constructor returns a
pointer, so every reference silently resolved to itself. Because all dereferencing funnels
through one adapter method, the fix was four lines in one place. Spread across callers, the
same bug would have been a corpus-wide mystery.

Replacing pdfcpu later means writing a second `objects.Store` implementation and changing
one constructor call. Nothing above `objects` changes. The interface is also what makes
in-memory test fakes possible, which is why `tag` can be tested against malformed structure
trees with no PDF files at all.
