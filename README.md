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
pdfops resize -to a4 odd-sizes.pdf a4.pdf
pdfops resize -to a4 -landscape slides.pdf wide.pdf
pdfops move -from 1 -to 3 misplaced.pdf ordered.pdf
pdfops split -every 10 book.pdf chapters/
pdfops reverse back-to-front.pdf right-way-round.pdf
pdfops nup -n 4 slides.pdf handout.pdf
pdfops booklet chapter.pdf to-fold.pdf
pdfops interleave both.pdf fronts.pdf backs.pdf
pdfops duplex scan.pdf document.pdf
pdfops onepage strip.pdf one-long-page.pdf
pdfops poster -across 2 -down 2 plan.pdf tiles.pdf
pdfops divide -across 2 spread.pdf pages.pdf
pdfops overlay -with letterhead.pdf plain.pdf headed.pdf
pdfops underlay -with watermark.pdf report.pdf behind.pdf
pdfops blank -before 3 report.pdf report-with-a-gap.pdf
pdfops watermark -text CONFIDENTIAL contract.pdf marked.pdf
pdfops number -format "{page} of {pages}" report.pdf numbered.pdf
pdfops bates -prefix ACME- -start 1 exhibits.pdf stamped.pdf
pdfops stamp -text "seen 25 August" -at top-right -bold notes.pdf seen.pdf
pdfops sanitize downloaded.pdf safe.pdf
pdfops flatten filled-form.pdf final.pdf
pdfops strip -annotations -bookmarks private.pdf clean.pdf
pdfops compress fat.pdf lean.pdf
pdfops encrypt -user letmein -allow print,copy plain.pdf locked.pdf
pdfops -password letmein decrypt locked.pdf plain.pdf
pdfops -password letmein permissions locked.pdf
pdfops text paper.pdf
pdfops text -layout -pages 1 paper.pdf
pdfops text -layout -json paper.pdf
pdfops tables accounts.pdf
pdfops tables -as md -pages 3 accounts.pdf
pdfops tables -as json -to tables/ accounts.pdf
pdfops markdown report.pdf > report.md
pdfops markdown -json report.pdf > report-for-indexing.json
pdfops images paper.pdf pictures/
pdfops attachments report.pdf
pdfops attachments -to files/ report.pdf
pdfops attach -file figures.csv -description "the figures" report.pdf with.pdf
pdfops detach -name figures.csv with.pdf without.pdf
pdfops outline -from toc.txt book.pdf bookmarked.pdf
pdfops outline -drop bookmarked.pdf plain.pdf
pdfops info file.pdf
pdfops metadata -set 'Title=The Report' -set Author=Someone draft.pdf titled.pdf
pdfops metadata -clear draft.pdf anonymous.pdf
pdfops version -set 1.7 old.pdf declared.pdf
```

A page range is written `1-3,7,10-` and may say `all`, `even`, `odd` or
`last`. It keeps its own order and its own repeats, so `select -pages 3-1`
reverses three pages and `select -pages 1,1` gives you two copies.

`-password` opens an encrypted file.

`interleave` takes one page from each file in turn, which is how two
single-sided scans — the fronts and the backs — become one double-sided
document. `onepage` stacks every page onto a single long one. `poster` cuts
each page into tiles so a plan can be printed larger than the paper and
assembled.

`deblank` finds the pages that hold nothing and drops them. It **draws** each
page and counts the pixels that are not paper, because that is the only test
that agrees with a reader: a page whose content stream is full of operators
painting white, or drawing outside the media box, looks blank and is blank, and
a page carrying a picture of nothing is blank too.

```sh
pdfops deblank -list scan.pdf          # say which, write nothing
pdfops deblank scan.pdf tidied.pdf
```

⛔ **The default resolution is 150 DPI, and it was raised from 50 because of a
measurement.** On A4:

| | 50 DPI | 150 DPI |
| --- | --- | --- |
| the page | 414 × 585 | 1240 × 1755 |
| one pixel | 4.1e-06 of it | 4.6e-07 |
| an 8-point full stop | **1 pixel** | 4 pixels |
| an 8-point comma | 2 pixels | 8 pixels |

At 50 DPI a full stop is *exactly the mark a speck of dust makes*, so no
threshold at that resolution can tell punctuation from dirt. At 150 there is a
factor of four to set `-max-ink` from, and `TestMeasureTheInkLadder` prints the
whole ladder so that a renderer change which moved either end fails there
instead of silently reclassifying somebody's pages.

⛔ **The zero value is the strictest setting**, not a middle one: a page with a
single inked pixel is kept. This is the input to deleting pages — a page
wrongly called blank destroys content, while a blank page wrongly kept merely
leaves a blank page. For scans, `-max-ink 1e-6` is about two pixels: it
forgives a speck and still keeps a page holding a full stop. It is not the
default because whether that margin holds depends on the scanner, which this
package cannot see.

Three more things it will not do:

- **it will not call a page of solid colour blank.** The test is distance from
  *white*, not uniformity; a measure of variance would delete a deliberate
  black separator sheet;
- **it will not empty the document.** Every page blank is an error, because a
  file of no pages still opens and still has a size, and a caller handed one
  would be told nothing was wrong;
- **it does not count annotations as ink** unless asked: a page whose only mark
  is a sticky note is blank paper somebody wrote on.

And `-list` writes nothing and takes no output file, because the first thing
anybody should do with a tool that deletes pages is ask it which ones it means.

⛔ `divide` makes the **same grid** as `poster` and is the opposite operation.
`poster` keeps every piece the size of the page it came from, so it
**enlarges**: the pieces taped together make one page the size of a wall.
`divide` keeps the content at its own size and makes the **pages** smaller, so
the pieces laid side by side are the page again at 1:1 — which is what a
two-page spread scanned as one wide sheet, or two tickets printed on one page,
needs.

Confusing them gives a result that looks right in a thumbnail and is wrong by a
factor of `across` and `down`. The tests assert the **media box**, not the page
count, and `TestDivideIsNotPoster` compares the two directly: without it, every
assertion about `divide` could be satisfied by `poster`.


`duplex` is the same job when the scanner left **one** file rather than two: a
feeder that cannot turn a sheet over gives every front, then every back. It
cuts the file in the middle and interleaves the halves.

⛔ **The backs are reversed by default**, and the flag is `-keep-backs` rather
than `-reverse-backs` so that the default is the common case: the stack is
turned over *as a stack*, so the last sheet's back is scanned first. A tool
whose default assumed otherwise would quietly put every back on the wrong
front — which reads as *"the scan is fine, the order is odd"* rather than as a
reversal.

The default split is `ceil(n/2)`, **not** `n/2`: an odd stack has its unpaired
sheet at the end of the **fronts**, so thirteen pages split at seven. ⛔ The
test for that had to be rewritten. The first version used a reversed back
block — and with the backs reversed the output is the same for any split in a
range, because the reversal absorbs the difference. It passed at the ceiling
**and at the floor**, so it proved nothing; the negative control is what said
so, and the assertion now uses `-keep-backs`, where the split is load-bearing.


`repair` writes a file back out with its structure rebuilt, and says whether
rebuilding was needed. `-check` answers that question and writes nothing.

```sh
pdfops repair -check suspect.pdf
pdfops repair broken.pdf fixed.pdf
```

⛔ **The rebuilding is not this verb's doing.**
[go-pdfkit/reader](https://github.com/go-pdfkit/reader) repairs a damaged file
on its own and quietly, which is the right behaviour for a reader — a caller
asking for page three wants page three, not a lecture. What was missing was a
way to **say** it happened and to keep the result, and that is the whole of
this verb. `Doc.Repaired()` is the same answer in Go.

It reports on **any** source, not all: a merged document's pages come from
several files, and one damaged source is the answer to *"did this need
repairing"*. A true that depended on every source being broken would be false
for every real merge.

⛔ And the scope is **reader's**, not QPDF's. What it recovers is a
cross-reference table that is wrong, missing, or points into the middle of
something else — the commonest damage, and the kind where the objects are all
still there. It is not a general salvage engine, and a file whose objects are
themselves truncated may still be unreadable. A file that did not need
repairing is **said out loud and is not an error**, so that "repair everything
in this directory" can be scripted.


`labels` sets what a viewer shows in its **toolbar** — "iv of 320" — which is
never what is printed on the paper. For a number on the page, that is `number`
above.

```sh
pdfops labels -list book.pdf
pdfops labels -rule 'pages=1-12;style=r' -rule 'pages=13-;style=D' book.pdf out.pdf
pdfops labels -rule 'pages=all;style=D;prefix=A-;start=1' appendix.pdf out.pdf
pdfops labels -clear book.pdf plain.pdf
```

The six styles are the format's own: `D` (1, 2, 3), `r` (i, ii, iii), `R` (I,
II, III), `a` (a, b, c), `A` (A, B, C), and an empty style, which is the prefix
alone — how a run of pages is marked "Cover" or "Insert".

⛔ **A page-label range in a PDF is CONTIGUOUS.** The file holds the first page
of each run and the viewer counts forward from there, so a rule over
`1-9,30-40` has to be *compiled* into two runs. Without `continue` the second
run starts again at `start`; with it, it carries on. `pages=odd` is the extreme
of the same thing — fifty runs of one page over a hundred — and both answers are
expressible only because a rule is compiled rather than stored as written.

⛔ **A page-label tree has no gaps either.** An entry labels from its own key
until the *next* entry, so a document labelled 1, 2 and then meant to stop
comes back 1, 2, 3, 4 unless the stop is said out loud: an entry with neither a
style nor a prefix. The first draft skipped unlabelled pages and the round-trip
caught it.

⛔ **`start` must be at least 1**, and that is the format's rule rather than a
choice here: ISO 32000-1 table 159 says `/St` "shall be greater than or equal
to 1". A zero-based numbering scheme is not expressible as a numeric style, so
a `start` below one is refused rather than written out of specification — a
file that opens in one viewer and renumbers itself in another is worse than a
refusal. BentoPDF's own tool offers "set to 0 for zero-based numbering", and
what that writes is a value the standard forbids.

⛔ **A page label's letters are not a spreadsheet's columns.** Table 159 says
the letters run "A to Z for the first 26 pages, AA to ZZ for the next 26" — so
27 is **AA** and 28 is **BB**, where a spreadsheet's 28 is AB. Borrowing the
spreadsheet rule, which is the one everybody has written before, relabels every
appendix past the twenty-sixth.

Roman numerals stop at 3999, where the additive notation runs out — 4000 needs
a bar over the M, which neither a PDF string nor a page number has — and past
it the number is written in decimal. A label a viewer shows *wrongly* is worse
than one it shows in the wrong style.

### ⛔ Every verb used to drop the page labels

A `/PageLabels` tree is keyed by page **index**, so it cannot be carried across
a rebuild the way the other document keys are: after a `select` or a `reverse`
every label would be on the wrong page. So this package dropped it — and a
document whose front matter was numbered i, ii, iii came back from a
**watermark** numbered 1, 2, 3.

The labels are now asked for **per page** and rebuilt, which is right in both
directions: a verb that keeps the pages keeps their labels, and a verb that
moves them moves the labels with them. A merge keeps each source's labels on
its own pages.

The runs are compressed on the way out: three hundred decimal pages are **one**
entry, not three hundred. A label no style can explain becomes a run of one with
the whole label as a literal prefix — the file then says exactly what the viewer
must show, at the cost of an entry.


`layers` is the optional content a viewer calls layers: a drawing's
dimensions, its markup, its alternatives.

```sh
pdfops layers -list plan.pdf
pdfops layers -hide 'Internal pricing' plan.pdf out.pdf
pdfops layers -add 'Markup/Mine' plan.pdf out.pdf
```

⛔ **Every verb used to lose them.** Measured before this: a one-page drawing
with a layer switched **off**, run through `pdfops watermark`, came back with
no `/OCProperties` at all — the content still *marked* as belonging to a layer,
the layer object still reachable through the page's own resources, and nothing
left saying which layer was hidden. **So the hidden layer showed.** A drawing
distributed with an "internal pricing" layer turned off revealed it to any verb
at all.

The layers are now constructed on the way out rather than carried, which is
what lets them survive a reorder *and* a merge: `keepCatalogue` refuses to
merge two catalogues, because two forms whose fields are named the same cannot
honestly become one — but two drawings' layers can, since a layer is its object
and two layers named "Markup" are two layers.

### What the file says about its own layers, and is obeyed

| | |
| --- | --- |
| `/BaseState OFF` | inverts the question: everything is hidden unless `/ON` names it. A reader that only looked at `/OFF` would report every layer of such a document as visible — and a drawing that ships with all its layers off but one is exactly how an alternatives document is built |
| `/Locked` | the document asking that a layer not be switched. `-hide` on one is **refused**: a drawing's dimension layer is what everything else is positioned against, and switching it anyway ignores the only thing the file says |
| `/D /Order` | the nesting, which is written back nested. A string inside it is the **group's label** rather than a layer |

⛔ **Dropping a hidden layer is refused**, because what was drawn on it does not
go with it: the content stays in the page and stops being optional, so the
hidden drawing becomes permanently **visible** — the opposite of what somebody
deleting a layer called "internal pricing" expects. Show it first if that is
what you mean.

⛔ **A name is not an identifier.** A drawing assembled from two others often
has two "Markup" layers, and switching one of them at random is a change
nobody asked for and nobody can see, so an ambiguous name is refused with the
count.

`tables` finds the grids a page's text lines up into, and writes them as CSV
(RFC 4180, a blank line between one table and the next), as JSON (with the page
and the box, so a caller knows where to look), or as GFM pipe tables.

```sh
pdfops tables accounts.pdf
pdfops tables -as md -pages 3 accounts.pdf
pdfops tables -as json -to tables/ accounts.pdf
```

A page holds no table — it holds text put at places — so this reports an
**inference**, and
[`extract`](https://github.com/go-pdfkit/extract#tables) is where the rules
are: a boundary is a stretch every row leaves empty, a repeated one-character
cell is a bullet and not a column, and a line far below the last row is not the
next one. Ruled lines are **not** read, so a table held apart by rules rather
than by whitespace is not found.

⛔ **A page that could not be read is said on the error stream**, not left to
look like a page with no tables on it — and on the error stream rather than
standard output, so that it cannot end up inside the rows somebody is about to
parse.

⛔ **The first row becomes the Markdown header.** A pipe table has nowhere to
put a table with no header row, and a grid found by looking at alignment does
not know whether its first row is one. The cells go through
[go-richdoc](https://github.com/go-richdoc/markdown) rather than through a
`strings.Join` here, so a cell holding a `|` is escaped by the code that
already knew it had to be.

⛔ **A document may say a word sits 10^308 points along**, and a table with one
word at minus that and another at plus it is infinitely wide. JSON has no
spelling for an infinity, so `-as json` **fails** on such a file rather than
writing `"width": +Inf` into something that has already begun parsing it.

`markdown` reads the pages back as a **document** — headings, paragraphs, lists
and tables — through
[`go-pdfkit/document`](https://github.com/go-pdfkit/document), and writes them
as Markdown. With `-json` it writes one record per page instead, each the
page's Markdown plus the document's own metadata, which is the shape a
retrieval pipeline already expects.

```sh
pdfops markdown report.pdf > report.md
pdfops markdown -pages 3-7 report.pdf
pdfops markdown -json report.pdf > report-for-indexing.json
```

⛔ **A PDF has no headings.** What this writes is an inference from how big the
glyphs were and where they sat, and `document`'s README says what it will and
will not see — a page in columns, or a table held apart by ruled lines, is not
read as either.

⛔ **A page range is applied after the document is read, never before.** What
counts as a heading is decided by the sizes the *whole document* uses, so
asking for page nine alone would otherwise give it different headings from
asking for all of them — and a caller doing a document a page at a time would
get a different answer for every page. A test asserts that page one reads the
same both ways.

⛔ **`-json` decodes the metadata.** A title written in UTF-16 behind a byte
order mark — which is most of what a modern producer writes — came out of the
old code as `\xfe\xffL\x00a\x00…`. `info` printed it that way too, for as long
as the verb had existed; both now go through
[`reader.TextString`](https://github.com/go-pdfkit/reader).

`textcolour` paints the text one colour and leaves the rest of the page alone.

```sh
pdfops textcolour -to '#0000ff' report.pdf blue-text.pdf
pdfops textcolour -to black -pages 1-4 scan.pdf fixed.pdf
```

⛔ **Setting a colour before each text object is not enough**, and that is what
the first draft did. A text object that sets its *own* colour overrides it, so
a page whose paragraphs say `1 0 0 rg` came out unchanged while the tool
reported success. Every colour operator **inside** a text object is blanked —
`g`, `rg`, `k`, `cs`, `sc`, `scn` and their stroking capitals — so the colour
asked for is the one that paints.

⛔ **The colour goes outside the text object, wrapped in `q`/`Q`.** A text
object does not save the graphics state, so a colour set inside one leaks into
whatever is drawn after it — which is how a recolouring turns a company's logo
the same shade as its paragraphs. A test asserts the rectangle beside the text
keeps its own red.

Both the filling **and** the stroking colour are set, because text has render
modes: 0 fills, 1 strokes, 2 does both.

⛔ **It splices, and does not re-serialise.** Writing the operations back out
would mean formatting every operand again, including an inline image's
still-encoded bytes, where a formatter that is merely lossless-for-objects can
change what the image *means*. So the rewriting keeps every byte it does not
deliberately change — which is why
[`reader.Operation`](https://github.com/go-pdfkit/reader) carries its byte
range, added for this. A stream with no text comes back **byte for byte**
unchanged, and the colour operators are **blanked with spaces** rather than
deleted so that every offset after one is still the offset the scan reported.

⛔ **What it cannot reach, it counts.** Text drawn through a Form XObject is
not recoloured, and the reason is not difficulty: an XObject is **shared**, so
recolouring one would change pages the caller did not name. A document whose
text is all inside XObjects would otherwise come back looking converted and be
unchanged — the one failure a caller cannot see by opening the result and
glancing at it. So the count is said out loud, and `Doc.UnreachedText` is the
same answer in Go.

And `#f00` is **full** red: each digit of a short hex colour is doubled, not
shifted, so the component is `0xf × 17`. A shift gives 240/255 for what every
browser calls full red, and a logo recoloured to 94 % red is a defect nobody
can see but a printer.

`attachments` lists the files a document carries, and writes them out with
`-to`. A name comes out of the document rather than from whoever runs the
command, so it is stripped to its last element: `../escaped.txt` is written
*inside* the directory asked for, and a name with no usable last element is
refused rather than skipped. `detach` refuses a name the file does not carry,
instead of writing an unchanged copy somebody then sends believing it was
removed.

`outline` replaces the bookmarks from a file, or removes them with `-drop`.
The file is one bookmark a line — a title, a tab, a page number — with two
spaces of indent for each level of nesting:

```
Introduction	1
  Why	2
Chapter one	7
```

A line it cannot read is an error naming the line, because a table of contents
quietly missing an entry is worse than one that refuses to be written.

`resize` sets how big a page **is**; `crop` sets what of it **shows**. Two
verbs, because they are two questions. A size is a box in points, or one of
`a3`, `a4`, `a5`, `letter`, `legal`, `tabloid` — derived from their definitions
rather than typed, so A4 is 210 × 297 mm at 72 points to the inch and not a
number somebody copied. `-landscape` turns a named size on its side.

`metadata` writes what `info` prints. A key is given the way the PDF names it,
with or without the leading slash, because a person reading `info` sees `Title`
and should be able to type what they saw.

`text -json` writes the same reading as the lines, in a shape something else
can parse without splitting on tabs. With `-layout` each run carries its
position, its size, and whether it was invisible or unreadable — and those two
are **written even when false**, because a reader that cannot tell "this run is
readable" from "this tool did not say" is reading a guess.

`text` reads the page back as words, and `-layout` says where every piece of
it sits — a page, a place, a size, and what it says. A piece the document
gave no way to read comes back marked rather than guessed at, so you can tell
a page that says nothing from one that could not be read. `images` writes out
the pictures a page places, a JPEG as a JPEG.

Links and bookmarks are carried over and pointed at the pages they became
here, so extracting three pages of a book leaves the links between those
three working and drops the ones that led out of them. A link or a bookmark
that already led nowhere in the source keeps its place without a
destination, because a file with broken bookmarks should keep its shape
rather than lose it.

The catalogue is always written from nothing, so a document-level script,
an action that runs on opening, or a tree of embedded files never survives
any operation at all. `sanitize` deals with what travels attached to a
page: its own actions, an annotation's actions, JavaScript, launching,
form submission, the annotation types that exist to play or embed
something, and files associated with a page. A link to the web is not
executable and stays.

### What a screen reader follows

A tagged document says which run of marks on which page is a heading, a
paragraph, a table cell, the label of a form field. That is the reading order a
screen reader follows, and for a government form it is often what the law
requires — RGAA in France, Section 508 in the United States, EN 301 549 in the
EU. It cannot be copied across a rebuild: every part of it points into the
document, and the number tree that indexes it is keyed by a number each page
carries. So it is rebuilt, element by element, around the pages that survived,
and those numbers are handed out afresh. A page that kept the number it had in
a file it is no longer part of is a page a reader would look up and be told,
with every confidence, about somebody else's.

An element whose every page has gone is removed and its parent pruned; one with
some pages gone keeps the children that are left. An empty table cell is kept,
because the shape of a table is part of what it says. A page written twice
carries the structure once, on the first copy, since an element names one page.
Merging two files carries no tree at all: two files have two role maps in which
the same name may mean two different things, and there is no honest way to
choose between them.

Of **1 633 real government forms** from eighteen issuers, 1 021 carry a
structure tree. After a rotate **1 014 still do** — the seven that do not are
the seven whose tree was empty in the file we were given — and **991 of the
1 021 are identical down to the last element, mark and annotation reference**.
The thirty that differ are accounted for one by one in the commit that added
this. What cannot be carried is named in `catalogue.go` with the reason, rather
than disappearing quietly.

Text is drawn in the four faces every viewer already has — Helvetica,
Helvetica-Bold, Courier and Courier-Bold — so nothing is embedded and a
watermark costs about a kilobyte. Stamp text may say `{page}`, `{pages}`
and `{n}`, the last being a counter for serial numbering.


## ⛔ What a rewrite carries, audited rather than assumed

Twice in one afternoon this package was found to be **dropping** a catalogue
entry whose absence is not "no opinion" but a **default** — so the loss changed
the document silently. So the whole list was measured: a document holding
twenty-three catalogue entries, a watermark over it, and of each entry the
question *what would its absence MEAN?*

| | |
| --- | --- |
| **`/PageLabels`** | the viewer numbers pages by position — a book's front matter came back 1, 2, 3 instead of i, ii, iii |
| **`/OCProperties`** | everything optional becomes **visible** — a hidden layer was revealed |
| **`/OutputIntents`** | **no colour management**: a prepress file lost the ICC profile it declares, and would print differently with nothing to say why |
| **`/URI`** | relative links resolve against wherever the viewer thinks the document is — the link is not removed, it is silently pointed elsewhere |

All four are carried now, the first two **rebuilt** (they cannot be copied: one
is keyed by page index, the other names objects the content reaches another
way) and the last two copied.

### What is deliberately not carried, and why

- **`/OpenAction`, `/AA`** — what the document does when it is opened. Dropping
  them is *safer*, and is what `sanitize` exists for: carrying them would carry
  **code** across a rebuild;
- **`/Legal`, `/DSS`** — a signature's attestation and its validation data. A
  rewrite has already invalidated the signature the moment it changed a byte,
  and carrying the validation data of a signature that no longer verifies would
  say the document is still attested when it is not;
- **`/Threads`, `/Collection`** — these point *into* the document, at pages and
  at embedded files, which is why `/Names /Dests` is not carried either.

⛔ And **three the audit reported as lost that are not**: `/AcroForm`,
`/Outlines` and `/StructTreeRoot` are all carried. The audit's fixture held an
**empty** one of each, and an empty form, outline or structure tree is
deliberately not written. *A measured absence has to be read before it is
believed* — which is the other half of why an audit is worth running.

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

## The rest of the family

[**reader**](https://github.com/go-pdfkit/reader) reads and writes the format ·
[**ops**](https://github.com/go-pdfkit/ops) is the verbs and the `pdfops` command ·
[**render**](https://github.com/go-pdfkit/render) turns a page into pixels ·
[**pdffont**](https://github.com/go-pdfkit/pdffont) is what a document says about a font ·
[**extract**](https://github.com/go-pdfkit/extract) reads a page back ·
[**coedit**](https://github.com/go-pdfkit/coedit) is a PDF several people edit at once ·
[**app**](https://github.com/go-pdfkit/app) is the browser workbench.

Documentation for all of it: <https://go-pdfkit.github.io/docs/>

## License

BSD-3-Clause — see [LICENSE](LICENSE). Copyright the go-pdfkit/ops authors.

## Forms

`pdfops fields` lists what a form asks for and what it holds; `pdfops fill`
fills it in and saves it.

```
$ pdfops fields fw9.pdf
note: the file also carries an XFA form, which is not read; the standard one is.
topmostSubform[0].Page1[0].f1_01[0]                       text      ""
topmostSubform[0].Page1[0].Boxes3a-b_ReadOrder[0].c1_1[0] checkbox  "Off"
                                                            buttons [1]

$ pdfops fill -set 'topmostSubform[0].Page1[0].f1_01[0]=Wolfgang Amadeus Mozart' \
              -set 'topmostSubform[0].Page1[0].Boxes3a-b_ReadOrder[0].c1_1[0]=1' \
              fw9.pdf filled.pdf
```

A filled form is written as an **incremental update**: the original file, byte
for byte, with the objects that changed appended after it and a new
cross-reference section pointing back at the old one. That is how every program
that saves a form saves one, and it is the safest thing a program can do to
somebody's document — nothing already there is rewritten, so whatever this does
not understand survives untouched, and if the update is wrong the original is
still the first part of the file.

The update says where its objects went **the same way the file already does**.
A file whose cross-reference is a stream cannot be pointed back at by a plain
table: a reader following `/Prev` would find an object where it expected the
word `xref`. That is not a nicety — macOS's own renderer draws nothing at all
for such a file, which is how the mistake was found.

Two things it will not do. A document that had to be **repaired** to be read has
no cross-reference section worth pointing back at, so it is refused rather than
added to. A document that is **encrypted** has every string and stream in it
written through a key, and this does not yet write into one.

Measured on a real form: `fw9.pdf` filled in every field, written out, read
back with every value in place, and rendered by macOS — which drew what we
drew, in the same places, comb cells and ticks included.

⚠ **Filling a form is the only verb that keeps one.** Every other verb here
takes the pages apart and builds a new document round them, and a form is tied
into a document by object number in a dozen places at once — so merging,
splitting or rotating a form's pages loses the form. Use `fill` on the file
itself.

## The form inside a blank document

Some PDFs are blank to look at and are not blank. Their pages hold one panel —
"Please wait... your PDF viewer may not be able to display this type of
document" — and the real form exists only as XML beside them, laid out when
Adobe's own reader opens it. Adobe removed the format from PDF 2.0; no browser
and no other reader lays one out.

`pdfops xfa` lays it out and draws it, which turns such a file into one
anything can show.

```
$ pdfops xfa t1135-fill-23e.pdf readable.pdf
6 sheets, 497 elements drawn
```

Measured over every dynamic form in a corpus of 2 240 real government forms —
fourteen of them, from the Canada Revenue Agency, the French cerfa series, the
US Department of Labor and the Office of Personnel Management: **all fourteen
laid out, 111 sheets, 9 808 elements, nothing left unplaced**, and every file
read back by poppler.

WHERE each element goes is [go-pdfkit/xfa](https://github.com/go-pdfkit/xfa)'s,
measured against pdf.js and pdfium. What is drawn INSIDE each box is this
package's own and much simpler: the four standard faces, a greedy line break,
a rule round each field. So this is **legibility rather than fidelity** — the
difference between a blank sheet and a form somebody can read, not between this
and Adobe.

**Pictures are drawn** — 27 of them across twelve of the fourteen. For most
that is a logo; for French cerfa 12064 it is the whole printed form. That one
carries 212 fields, 7 draws, 4 images and — counted, not guessed — no `<text>`
and no `<caption>` whatever, so without its pictures it is a grid of empty
boxes and with them it is the customs declaration it is. How big each one is
drawn follows pdfium's own arithmetic (`xfa/fxfa/cxfa_ffwidget.cpp:55-86`),
because pdf.js declines to settle `fit` and `actual` and leaves them to a
browser's sizing of an `<img>`.

**A picture's own size is its pixels at its own resolution**, read from a PNG's
`pHYs` chunk or a JPEG's JFIF density, and that is not a detail. Every Canada
Revenue Agency form in the corpus writes `aspect="actual"`, and their logos are
1200-dpi PNGs: 2617 pixels across is 157.02 points, which is to a hundredth of
a point the width the template writes for the box. Taken at 72 dpi — one point
per pixel, the assumption for a picture that declares nothing — the same logo
is 2617 points wide and covers a third of the sheet in black. It did, until the
render was looked at.

**Page numbers come out right** — "Page 3 of 11", and "Page 3 sur 4" on a
French form — without running the form's scripts. A footer like that is not
text: it is rich text holding a *floating field* naming a hidden field whose
value a script sets. All sixteen of those in the corpus ask one of exactly two
things:

```
this.rawValue = xfa.layout.page(this);
this.rawValue = xfa.layout.pageCount();
```

Those are questions asked **of the layout**, which has just answered them.
Recognising the two and handing back what the pager computed is not evaluating
anything. Neither reference does this — pdf.js's layout ignores `xfa:embed`
entirely, pdfium runs a JavaScript engine — so it is named as ours. Anything
else a floating field names is left as a gap, because a wrong number that looks
right is worse than a blank: four cerfa fields carry a static default beside
their script, and printing that would put "Page 1 of 1" on every sheet.

Two limits, both named rather than left to be discovered:

- **A picture in a file outside the document is not fetched.** That is pdf.js's
  position and its words: *"we don't get remote data and use what we have in the
  pdf itself, so no picture for non null href"*. Two of cerfa 12818's are like
  that, and both name an absolute path on the machine of whoever drew the form
  — `C:\Users\…\Downloads\logo ministere.PNG`. They are reported, not passed
  over.
- **Text is set smaller rather than cut off.** The box was measured with the
  font the template names and this draws in Helvetica, which is wider; at the
  template's own size the words come to more lines than the box holds. The size
  comes down until they fit, with a floor of four points, because losing the
  end of a sentence is worse than setting it a point smaller.
