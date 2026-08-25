# ops

[![CI](https://github.com/go-pdfkit/ops/actions/workflows/ci.yml/badge.svg)](https://github.com/go-pdfkit/ops/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/go-pdfkit/ops.svg)](https://pkg.go.dev/github.com/go-pdfkit/ops)
[![Go Report Card](https://goreportcard.com/badge/github.com/go-pdfkit/ops)](https://goreportcard.com/report/github.com/go-pdfkit/ops)
[![License: BSD-3-Clause](https://img.shields.io/badge/License-BSD--3--Clause-blue.svg)](LICENSE)
[![Coverage](https://img.shields.io/badge/coverage-100%25-brightgreen.svg)](#testing)

The verbs of [go-pdfkit](https://github.com/go-pdfkit): what people actually do
to a PDF they already have. Merge two files, pull out pages three to seven,
turn a page the right way up, drop the metadata, split a report into chapters.

Built on [`reader`](https://github.com/go-pdfkit/reader), which takes a file
apart and puts one back together; nothing outside the Go standard library is
used, so this builds for `GOOS=js/wasm` and every architecture the fleet
targets.

## The model

A document here is **an ordered list of pages, each borrowed from a source
file**, plus the document-level pieces. Every operation rearranges that list or
annotates its entries; nothing is applied until the document is written out.
An operation therefore costs nothing until it has to, several files can be
mixed freely, and — the reason the model is shaped this way — the list is a
plain value that two people can edit at the same time.

## What it does

```sh
go install github.com/go-pdfkit/ops/cmd/pdfops@latest

pdfops merge whole.pdf part1.pdf part2.pdf
pdfops select -pages 3-7,last report.pdf extract.pdf
pdfops delete -pages even scan.pdf fronts.pdf
pdfops rotate -pages all -by 90 sideways.pdf upright.pdf
pdfops crop -box 20,20,575,820 wide.pdf trimmed.pdf
pdfops split -every 10 book.pdf chapters/
pdfops reverse back-to-front.pdf right-way-round.pdf
pdfops nup -n 4 slides.pdf handout.pdf
pdfops booklet chapter.pdf to-fold.pdf
pdfops overlay -with letterhead.pdf plain.pdf headed.pdf
pdfops blank -before 3 report.pdf report-with-a-gap.pdf
pdfops strip private.pdf clean.pdf
pdfops info file.pdf
```

A page range is written `1-3,7,10-` and may say `all`, `even`, `odd` or
`last`. It keeps its own order and its own repeats, so `select -pages 3-1`
reverses three pages and `select -pages 1,1` gives you two copies.

`-password` opens an encrypted file; what is written out is not encrypted.

## Verified against real files

Every operation is checked on the same corpus of **118 863 real PDFs** the
reader is measured against — Matplotlib, cairo, pdfTeX, Ghostscript, Adobe,
R, Apache FOP, PDF 1.3 through 1.7. For each of the **118 833** that open,
four properties have to hold, compared on the bytes of every page's content
stream rather than on an exit status:

- writing the document unchanged reproduces every page;
- reversing twice is the identity;
- merging a document with itself doubles it exactly;
- selecting the last page yields exactly that page.

**All four hold on all 118 833 files.**

Laying pages out is checked the same way: for each of the **1 959**
multi-page files in that corpus, two-up produces the right number of sheets
and every form drawn on them carries exactly the bytes of the page it stands
for, blanks included. **All 1 959 hold.**

## Testing

```sh
go test -covermode=set ./...
```

CI gates on **exact 100% statement coverage**, `go vet`, and a cross-compile
across `linux/{amd64,arm64,riscv64,loong64,ppc64le,s390x}`, `js/wasm`,
`darwin/arm64` and `windows/amd64`.

## License

BSD-3-Clause — see [LICENSE](LICENSE). Copyright the go-pdfkit/ops authors.
