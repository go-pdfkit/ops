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

## Testing

```sh
go test -covermode=set ./...
```

CI gates on **exact 100% statement coverage**, `go vet`, and a cross-compile
across `linux/{amd64,arm64,riscv64,loong64,ppc64le,s390x}`, `js/wasm`,
`darwin/arm64` and `windows/amd64`.

## License

BSD-3-Clause — see [LICENSE](LICENSE). Copyright the go-pdfkit/ops authors.
