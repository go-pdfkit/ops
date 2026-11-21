package ops

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/go-pdfkit/reader"
)

// smallSize is classic-xref.pdf — linearized.pdf rewritten by `qpdf --qdf
// --object-streams=disable`, nine objects and two pages — with its trailer
// saying it holds two objects, the offsets left as they were.
func smallSize(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "classic-qdf.pdf"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, []byte("/Size 10")) {
		t.Fatal("the fixture does not say /Size 10")
	}
	return bytes.Replace(b, []byte("/Size 10"), []byte("/Size 03"), 1)
}

// pagesOf reads every page dictionary of a file.
func pagesOf(t *testing.T, b []byte) []reader.Dict {
	t.Helper()
	d, err := reader.Open(b)
	if err != nil {
		t.Fatal(err)
	}
	var pages []reader.Dict
	for i := 1; i <= d.PageCount(); i++ {
		p, err := d.Page(i)
		if err != nil {
			t.Fatal(err)
		}
		pages = append(pages, p)
	}
	return pages
}

// TestATrailerSizeThatIsTooSmallDoesNotLetTheUpdateOverwrite is the review's
// repro: the objects were numbered from /Size, so with a /Size of 3 the
// signature landed on objects 3, 4 and 5 — a page among them — and pdfsig
// still called the file valid.
func TestATrailerSizeThatIsTooSmallDoesNotLetTheUpdateOverwrite(t *testing.T) {
	src := smallSize(t)
	before := pagesOf(t, src)
	if len(before) != 2 {
		t.Fatalf("the fixture has %d pages", len(before))
	}
	out, err := signOrFail(t, src, opts(t, "ecdsa"))
	if err != nil {
		t.Fatal(err)
	}
	after := pagesOf(t, out)
	for i, p := range after {
		if p.Get("Type") != reader.Name("Page") {
			t.Errorf("page %d is no longer a page: %v", i+1, p)
		}
	}
	// The first page gains the widget; everything else about it is as it was.
	if !reflect.DeepEqual(after[1], before[1]) {
		t.Errorf("page 2 changed: %v became %v", before[1], after[1])
	}
	// Every object the file had is still what it was, bar the three an update of
	// this kind rewrites on purpose: the catalogue and the first page.
	was, _ := reader.Open(src)
	now, _ := reader.Open(out)
	root := was.Trailer().Get("Root").(reader.Ref)
	page1, _ := was.PageRef(1)
	for num := 1; num <= 9; num++ {
		ref := reader.Ref{Num: num}
		if ref == root || ref == page1 {
			continue
		}
		a, _ := was.Get(ref)
		b, _ := now.Get(ref)
		if !reflect.DeepEqual(a, b) {
			t.Errorf("object %d was overwritten: %v became %v", num, a, b)
		}
	}
	if size, _ := reader.ToInt(now.Trailer().Get("Size")); size < 12 {
		t.Errorf("the update says the file holds %d objects, and it holds at least 12", size)
	}
	if len(ours(t, out, 1)) != 1 {
		t.Error("the signature does not verify")
	}

	qpdf := tool(t, "qpdf", "REQUIRE_QPDF")
	p := filepath.Join(t.TempDir(), "small-size-signed.pdf")
	if err := os.WriteFile(p, out, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := exec.Command(qpdf, "--check", p).CombinedOutput()
	if err != nil || strings.Contains(string(got), "WARNING") || strings.Contains(string(got), "ERROR") {
		t.Errorf("qpdf --check: %v\n%s", err, got)
	}
}

// TestATrailerSizeLargerThanTheSectionsIsKept: a /Size above what the sections
// list is believed, since numbers below it may be spoken for elsewhere.
func TestATrailerSizeLargerThanTheSectionsIsKept(t *testing.T) {
	src := bytes.Replace(smallSize(t), []byte("/Size 03"), []byte("/Size 50"), 1)
	out, err := signOrFail(t, src, opts(t, "ecdsa"))
	if err != nil {
		t.Fatal(err)
	}
	d, _ := reader.Open(out)
	if size, _ := reader.ToInt(d.Trailer().Get("Size")); size < 52 {
		t.Errorf("the update says %d objects, and /Size 50 promised 50 before it", size)
	}
	for _, huge := range []string{"/Size 2147483647", "/Size 99999999999"} {
		b := bytes.Replace(smallSize(t), []byte("/Size 03"), []byte(huge), 1)
		out, err := signOrFail(t, b, opts(t, "ecdsa"))
		if err != nil {
			t.Fatalf("%s: %v", huge, err)
		}
		d, _ := reader.Open(out)
		if size, _ := reader.ToInt(d.Trailer().Get("Size")); size > 100 {
			t.Errorf("%s: believed, the update says %d objects", huge, size)
		}
	}
	// A stream section whose /Length is indirect is read without looking
	// anything up.
	b := []byte("%PDF-1.5\n5 0 obj\n<< /Type /XRef /Length 7 0 R /Size 8 >>\nstream\n\nendstream\nendobj\n")
	if got := xrefHighest(b, 9); got != 0 && got != 7 {
		t.Errorf("highest %d", got)
	}
}

// TestFillingNumbersObjectsFromTheSectionsToo: filling goes through the same
// numbering as signing, so a /Size that is too small does not let a filled
// field's appearance land on an existing object either.
func TestFillingNumbersObjectsFromTheSectionsToo(t *testing.T) {
	src := sigFieldFile(t, false)
	i := bytes.LastIndex(src, []byte("/Size "))
	if i < 0 {
		t.Fatal("no /Size in the fixture")
	}
	j := i + len("/Size ")
	k := j
	for src[k] >= '0' && src[k] <= '9' {
		k++
	}
	count, _ := strconv.Atoi(string(src[j:k]))
	small := append(append(append([]byte(nil), src[:j]...), '2'), src[k:]...)
	f, ok, err := OpenForm(small)
	if err != nil || !ok {
		t.Fatalf("%v %v", ok, err)
	}
	if err := f.Fill("name", "Marie"); err != nil {
		t.Fatal(err)
	}
	out, err := f.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	was, _ := reader.Open(small)
	now, err := reader.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	fld, _ := f.Form().Field("name")
	ref, _ := fld.Ref()
	for num := 1; num < count; num++ {
		a, _ := was.Get(reader.Ref{Num: num})
		b, _ := now.Get(reader.Ref{Num: num})
		if !reflect.DeepEqual(a, b) && num != ref.Num {
			t.Errorf("object %d was overwritten: %v became %v", num, a, b)
		}
	}
}

// TestXrefHighest reads the sections of files this package writes, whichever
// kind each is, along the whole chain.
func TestXrefHighest(t *testing.T) {
	for _, in := range inputs(t) {
		t.Run(in.name, func(t *testing.T) {
			o := opts(t, "ecdsa")
			o.Field = in.field
			once, err := signOrFail(t, in.b, o)
			if err != nil {
				t.Fatal(err)
			}
			o2 := opts(t, "ecdsa")
			o2.Field = "second"
			twice, err := signOrFail(t, once, o2)
			if err != nil {
				t.Fatal(err)
			}
			for _, b := range [][]byte{in.b, once, twice} {
				at, _ := lastStartxref(b)
				d, _ := reader.Open(b)
				size, _ := reader.ToInt(d.Trailer().Get("Size"))
				// The writers say the truth in /Size, and a stream section lists
				// itself, so the number of the highest is one less than it.
				if got := xrefHighest(b, at); got != int(size)-1 {
					t.Errorf("highest %d, /Size %d", got, size)
				}
			}
		})
	}
	// The earlier section is only reached through /Prev: the newest one lists
	// the objects it rewrote, not the whole file's.
	src := smallSize(t)
	out, _ := signOrFail(t, src, opts(t, "ecdsa"))
	at, _ := lastStartxref(out)
	if _, h := tableSection(out, at); h < 11 {
		t.Errorf("the update's own section lists up to %d", h)
	}
	if got := xrefHighest(out, at); got != 11 && got != 12 {
		t.Errorf("the chain's highest is %d, want 11 or 12 (nine objects, then the update's)", got)
	}
	if got := xrefHighest(src, mustStartxref(t, src)); got != 9 {
		t.Errorf("highest of the original is %d, want 9 although /Size says 3", got)
	}
}

func mustStartxref(t *testing.T, b []byte) int {
	t.Helper()
	at, ok := lastStartxref(b)
	if !ok {
		t.Fatal("no startxref")
	}
	return at
}

// TestOpeningAFormRefusesWhatCannotBeAddedTo asserts each refusal of the
// openers, by what it says, since each is followed by code that would crash on
// what it refused.
func TestOpeningAFormRefusesWhatCannotBeAddedTo(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("panicked instead of refusing: %v", r)
		}
	}()
	if _, ok, err := OpenForm([]byte("not a pdf")); ok || err == nil {
		t.Errorf("garbage was opened: %v %v", ok, err)
	}
	if _, ok, err := OpenFormWithPassword([]byte("not a pdf"), ""); ok || err == nil {
		t.Errorf("garbage was opened with a password: %v %v", ok, err)
	}
	// A form file whose startxref is damaged is read by repair, and cannot be
	// added to; it is refused by saying so, not by the repair's own message.
	damaged := bytes.Replace(sigFieldFile(t, false), []byte("startxref"), []byte("startxrex"), 1)
	_, ok, err := OpenForm(damaged)
	if ok || !errors.Is(err, ErrNotAppendable) || !strings.Contains(err.Error(), "does not say where") {
		t.Errorf("a file with no startxref: %v %v", ok, err)
	}
}

// TestXrefHighestBounds: the walk stops after maxSections sections, and an
// offset of 0 is no section.
func TestXrefHighestBounds(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("read what is not a section: %v", r)
		}
	}()
	// Section k, counting from the newest, lists object k; the newest is last
	// in the file and each /Prev points at the next older, which came before.
	n := maxSections + 2
	b := []byte("%PDF-1.4\n")
	prev := -1
	var newest int
	for k := n - 1; k >= 0; k-- {
		at := len(b)
		b = append(b, "xref\n"+strconv.Itoa(k)+" 1\n0000000000 00000 n \ntrailer\n<< "...)
		if prev >= 0 {
			b = append(b, "/Prev "+strconv.Itoa(prev)...)
		}
		b = append(b, " >>\n"...)
		prev, newest = at, at
	}
	if got := xrefHighest(b, newest); got != maxSections-1 {
		t.Errorf("highest %d after %d sections, want %d", got, maxSections, maxSections-1)
	}
	// A section at offset 0 is not one: nothing is read from the start of the file.
	zero := []byte("xref\n0 3\n" + strings.Repeat("0000000000 00000 n \n", 3) + "trailer\n<<>>\n")
	if got := xrefHighest(zero, 0); got != 0 {
		t.Errorf("an offset of 0 was read: highest %d", got)
	}
	if got := xrefHighest(zero, 1); got != 0 {
		// Offset 1 is "ref..." — a table by the reader's own test, with no
		// "xref" word to begin it, and so nothing is listed.
		t.Errorf("highest %d", got)
	}
}

// TestTheLargestFileAByteRangeCanDescribeIsSigned: at the limit, not past it.
func TestTheLargestFileAByteRangeCanDescribeIsSigned(t *testing.T) {
	defer func(n int64) { maxSignedFile = n }(maxSignedFile)
	src := sigFieldFile(t, false)
	// A fixed reserve: a measured one varies with the signature's length, and
	// so does the file's.
	o := opts(t, "ecdsa")
	o.Reserve = 2048
	whole, err := signOrFail(t, src, o)
	if err != nil {
		t.Fatal(err)
	}
	maxSignedFile = int64(len(whole))
	if _, err := signOrFail(t, src, o); err != nil {
		t.Errorf("a file of exactly the limit was refused: %v", err)
	}
	maxSignedFile = int64(len(whole)) - 1
	if _, err := signOrFail(t, src, o); err == nil {
		t.Error("a file one byte over the limit was signed")
	}
}

// TestXrefHighestOfAHybridFile follows /XRefStm from a classic section into a
// stream one.
func TestXrefHighestOfAHybridFile(t *testing.T) {
	packed := formFile(t, true, func(*reader.Writer, reader.Ref) (reader.Dict, reader.Array) { return nil, nil })
	stm := mustStartxref(t, packed)
	want := xrefHighest(packed, stm)
	if want < 3 {
		t.Fatalf("the stream section lists up to %d", want)
	}
	hybrid := func(trailer string) []byte {
		b := append([]byte(nil), packed...)
		at := len(b) + 1
		b = append(b, "\nxref\n0 1\n0000000000 65535 f \ntrailer\n<< /Size 1 "+trailer+" >>\nstartxref\n"+strconv.Itoa(at)+"\n%%EOF\n"...)
		return b
	}
	if got := xrefHighest(hybrid("/XRefStm "+strconv.Itoa(stm)), len(packed)+1); got != want {
		t.Errorf("a hybrid file's highest is %d, want %d (the stream's)", got, want)
	}
	for name, trailer := range map[string]string{
		"none":         "",
		"zero":         "/XRefStm 0",
		"past the end": "/XRefStm 99999999",
		"itself":       "/XRefStm " + strconv.Itoa(len(packed)+1),
	} {
		if got := xrefHighest(hybrid(trailer), len(packed)+1); got != 0 {
			t.Errorf("%s: highest %d, want 0 (only object 0 is listed)", name, got)
		}
	}
}

// TestXrefHighestRefusesToBelieveWhatIsGarbage: a section that cannot be read
// ends the walk with what was found, and promises beyond the file's end or an
// int are not believed.
func TestXrefHighestRefusesToBelieveWhatIsGarbage(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("panicked: %v", r)
		}
	}()
	entry := "0000000000 00000 n \n"
	for name, c := range map[string]struct {
		in   string
		want int
	}{
		"empty":                   {"", 0},
		"not a section":           {"hello", 0},
		"words for numbers":       {"xref\nabc def\n", 0},
		"a word for first":        {"xref\nabc 3\n" + entry + entry + entry + "trailer\n<<>>", 0},
		"negative first":          {"xref\n-1 2\n" + entry + entry, 0},
		"negative count":          {"xref\n0 -2\n", 0},
		"first too large":         {"xref\n2147483647 1\n" + entry, 0},
		"first at the bound":      {"xref\n2147483646 1\n" + entry + "trailer\n<<>>", 2147483646},
		"count at the bound":      {"xref\n1 2147483646\n" + entry, 1},
		"an empty subsection":     {"xref\n0 0\n5 1\n" + entry + "trailer\n<<>>", 5},
		"count too large":         {"xref\n0 2147483647\n" + entry, 0},
		"count past the file":     {"xref\n0 9\n" + entry + entry, 1},
		"entries then no trailer": {"xref\n0 2\n" + entry + entry, 1},
		"a subsection from 5":     {"xref\n5 2\n" + entry + entry + "trailer\n<<>>", 6},
		"two subsections":         {"xref\n0 1\n" + entry + "7 1\n" + entry + "trailer\n<<>>", 7},
		"a lower one after":       {"xref\n7 1\n" + entry + "0 1\n" + entry + "trailer\n<<>>", 7},
	} {
		b := []byte("%PDF-1.4\n" + c.in)
		if got := xrefHighest(b, 9); got != c.want {
			t.Errorf("%s: highest %d, want %d", name, got, c.want)
		}
	}
	// A stream section: /Index when it has one, /Size when it has not, nothing
	// that is not a number.
	for name, c := range map[string]struct {
		dict string
		want int
	}{
		"index":          {"/Index [0 3 10 2]", 11},
		"index backward": {"/Index [10 2 0 3]", 11},
		"odd index":      {"/Index [4]", 0},
		"index too big":  {"/Index [0 2147483647]", 0},
		"index negative": {"/Index [-1 5]", 0},
		"index no count": {"/Index [3 0]", 0},
		"size":           {"/Size 8", 7},
		"size too big":   {"/Size 2147483647", 0},
		"size zero":      {"/Size 0", 0},
		"neither":        {"", 0},
	} {
		b := []byte("%PDF-1.5\n5 0 obj\n<< /Type /XRef /Length 0 " + c.dict + " >>\nstream\n\nendstream\nendobj\n")
		if got := xrefHighest(b, 9); got != c.want {
			t.Errorf("stream %s: highest %d, want %d", name, got, c.want)
		}
	}
	// A cross-reference "stream" that is not one, or does not parse.
	for name, in := range map[string]string{
		"a dictionary": "%PDF-1.5\n5 0 obj\n<< /Type /XRef /Size 8 >>\nendobj\n",
		"an integer":   "%PDF-1.5\n5 0 obj\n42\nendobj\n",
		"garbage":      "%PDF-1.5\nhello",
	} {
		if got := xrefHighest([]byte(in), 9); got != 0 {
			t.Errorf("%s: highest %d, want 0", name, got)
		}
	}
	// /Prev loops and wild offsets end the walk.
	loop := []byte("%PDF-1.4\nxref\n0 1\n0000000000 65535 f \ntrailer\n<< /Prev 9 >>\n")
	if got := xrefHighest(loop, 9); got != 0 {
		t.Errorf("a section that names itself: highest %d", got)
	}
	for _, prev := range []string{"0", "-4", "9999999"} {
		b := []byte("%PDF-1.4\nxref\n0 3\n" + strings.Repeat("0000000000 65535 f \n", 3) + "trailer\n<< /Prev " + prev + " >>\n")
		if got := xrefHighest(b, 9); got != 2 {
			t.Errorf("/Prev %s: highest %d, want 2", prev, got)
		}
	}
	// A real chain of two sections, the older listing more than the newer.
	// The newer one's highest is 0, and only /Prev reaches the 5.
	head := "%PDF-1.4\n"
	older := "xref\n0 6\n" + strings.Repeat("0000000000 65535 f \n", 6) + "trailer\n<< >>\n"
	chain := []byte(head + older + "xref\n0 1\n0000000000 65535 f \ntrailer\n<< /Prev " + strconv.Itoa(len(head)) + " >>\n")
	if got := xrefHighest(chain, len(head)+len(older)); got != 5 {
		t.Errorf("a chain of two: highest %d, want 5", got)
	}
}
