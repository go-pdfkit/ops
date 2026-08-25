package ops

import (
	"testing"

	"github.com/go-pdfkit/reader"
)

func open(t *testing.T, n int) *Doc {
	t.Helper()
	d, err := Open(simple(t, n))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestOpenAndCount(t *testing.T) {
	d := open(t, 3)
	if got := d.PageCount(); got != 3 {
		t.Errorf("PageCount() = %d", got)
	}
	if got := d.Version(); got != "1.7" {
		t.Errorf("Version() = %q", got)
	}
	if got := written(t, d); !equal(got, pages(1, 2, 3)) {
		t.Errorf("pages = %v", got)
	}
}

func TestOpenRejectsRubbish(t *testing.T) {
	if _, err := Open([]byte("not a pdf")); err == nil {
		t.Error("want an error")
	}
	if _, err := OpenWithPassword([]byte("not a pdf"), "x"); err == nil {
		t.Error("want an error")
	}
}

func TestSelect(t *testing.T) {
	d := open(t, 5)
	if err := d.Select("4-2"); err != nil {
		t.Fatal(err)
	}
	if got := written(t, d); !equal(got, pages(4, 3, 2)) {
		t.Errorf("pages = %v", got)
	}
	if err := open(t, 5).Select("9"); err == nil {
		t.Error("an out-of-range selection should fail")
	}
}

func TestSelectDuplicates(t *testing.T) {
	d := open(t, 2)
	if err := d.Select("1,1,2,1"); err != nil {
		t.Fatal(err)
	}
	if got := written(t, d); !equal(got, pages(1, 1, 2, 1)) {
		t.Errorf("pages = %v", got)
	}
}

func TestDelete(t *testing.T) {
	d := open(t, 5)
	if err := d.Delete("2,4"); err != nil {
		t.Fatal(err)
	}
	if got := written(t, d); !equal(got, pages(1, 3, 5)) {
		t.Errorf("pages = %v", got)
	}
	if err := open(t, 5).Delete("nonsense"); err == nil {
		t.Error("a bad range should fail")
	}
}

func TestReverse(t *testing.T) {
	d := open(t, 4)
	d.Reverse()
	if got := written(t, d); !equal(got, pages(4, 3, 2, 1)) {
		t.Errorf("pages = %v", got)
	}
	d.Reverse()
	if got := written(t, d); !equal(got, pages(1, 2, 3, 4)) {
		t.Errorf("reversing twice = %v", got)
	}
}

func TestMove(t *testing.T) {
	d := open(t, 4)
	if err := d.Move(1, 3); err != nil {
		t.Fatal(err)
	}
	if got := written(t, d); !equal(got, pages(2, 3, 1, 4)) {
		t.Errorf("pages = %v", got)
	}
	d = open(t, 4)
	if err := d.Move(4, 1); err != nil {
		t.Fatal(err)
	}
	if got := written(t, d); !equal(got, pages(4, 1, 2, 3)) {
		t.Errorf("pages = %v", got)
	}
	if err := d.Move(0, 1); err == nil {
		t.Error("moving from nowhere should fail")
	}
	if err := d.Move(1, 9); err == nil {
		t.Error("moving to nowhere should fail")
	}
}

func TestMergeAndAppend(t *testing.T) {
	a := open(t, 2)
	b := open(t, 3)
	got := written(t, Merge(a, b))
	if !equal(got, append(pages(1, 2), pages(1, 2, 3)...)) {
		t.Errorf("pages = %v", got)
	}
	// Merging keeps the higher version and the first information dictionary.
	a.SetVersion("1.4")
	b.SetVersion("2.0")
	m := Merge(a, b)
	if m.Version() != "2.0" {
		t.Errorf("version = %q", m.Version())
	}
	if m.Info() == nil {
		t.Error("the information dictionary was lost")
	}
}

func TestMergeIntoAnEmptyDocument(t *testing.T) {
	if got := New().PageCount(); got != 0 {
		t.Errorf("PageCount() = %d", got)
	}
	if _, err := New().Bytes(); err == nil {
		t.Error("writing nothing should fail")
	}
}

func TestRotate(t *testing.T) {
	d := open(t, 2)
	if err := d.Rotate("1", 90); err != nil {
		t.Fatal(err)
	}
	if err := d.Rotate("1", 90); err != nil {
		t.Fatal(err)
	}
	if got, _ := d.Rotation(1); got != 180 {
		t.Errorf("page 1 rotation = %d", got)
	}
	if got, _ := d.Rotation(2); got != 0 {
		t.Errorf("page 2 rotation = %d", got)
	}
	if err := d.Rotate("all", -90); err != nil {
		t.Fatal(err)
	}
	if got, _ := d.Rotation(1); got != 90 {
		t.Errorf("after -90, page 1 = %d", got)
	}
	if got, _ := d.Rotation(2); got != 270 {
		t.Errorf("after -90, page 2 = %d", got)
	}
	if err := d.Rotate("all", 45); err == nil {
		t.Error("45 degrees should fail")
	}
	if err := d.Rotate("x", 90); err == nil {
		t.Error("a bad range should fail")
	}
	if _, err := d.Rotation(0); err == nil {
		t.Error("a page out of range should fail")
	}
}

func TestRotationSurvivesWriting(t *testing.T) {
	d := open(t, 1)
	if err := d.Rotate("all", 270); err != nil {
		t.Fatal(err)
	}
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	back, err := Open(out)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := back.Rotation(1); got != 270 {
		t.Errorf("rotation = %d", got)
	}
}

func TestSetRotation(t *testing.T) {
	d := open(t, 2)
	if err := d.Rotate("all", 90); err != nil {
		t.Fatal(err)
	}
	if err := d.SetRotation("2", 180); err != nil {
		t.Fatal(err)
	}
	if got, _ := d.Rotation(1); got != 90 {
		t.Errorf("page 1 = %d", got)
	}
	if got, _ := d.Rotation(2); got != 180 {
		t.Errorf("page 2 = %d", got)
	}
	if err := d.SetRotation("all", 45); err == nil {
		t.Error("45 degrees should fail")
	}
	if err := d.SetRotation("x", 90); err == nil {
		t.Error("a bad range should fail")
	}
}

func TestRotationOfASourceFile(t *testing.T) {
	b := buildPDF(t, 2, func(i int, d reader.Dict) {
		switch i {
		case 1:
			d["Rotate"] = reader.Integer(-90)
		case 2:
			d["Rotate"] = reader.Integer(45) // not a rotation the format allows
		}
	})
	d, err := Open(b)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := d.Rotation(1); got != 270 {
		t.Errorf("page 1 = %d", got)
	}
	if got, _ := d.Rotation(2); got != 0 {
		t.Errorf("page 2 = %d", got)
	}
}

func TestRotationOfAnUnreadableEntry(t *testing.T) {
	b := buildPDF(t, 1, func(i int, d reader.Dict) { d["Rotate"] = reader.Name("sideways") })
	d, err := Open(b)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := d.Rotation(1); got != 0 {
		t.Errorf("rotation = %d", got)
	}
}

func TestCropAndResize(t *testing.T) {
	d := open(t, 2)
	if err := d.Crop("1", [4]float64{10, 10, 90, 190}); err != nil {
		t.Fatal(err)
	}
	if err := d.Resize("2", [4]float64{0, 0, 300, 400}); err != nil {
		t.Fatal(err)
	}
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	src, err := reader.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	one, _ := src.Page(1)
	if got := string(reader.FormatObject(one.Get("CropBox"))); got != "[10 10 90 190]" {
		t.Errorf("CropBox = %s", got)
	}
	two, _ := src.Page(2)
	if got := string(reader.FormatObject(two.Get("MediaBox"))); got != "[0 0 300 400]" {
		t.Errorf("MediaBox = %s", got)
	}
	for _, bad := range [][4]float64{{10, 0, 10, 5}, {0, 10, 5, 10}} {
		if err := d.Crop("all", bad); err == nil {
			t.Errorf("%v is not a box", bad)
		}
		if err := d.Resize("all", bad); err == nil {
			t.Errorf("%v is not a box", bad)
		}
	}
	if err := d.Crop("x", [4]float64{0, 0, 1, 1}); err == nil {
		t.Error("a bad range should fail")
	}
	if err := d.Resize("x", [4]float64{0, 0, 1, 1}); err == nil {
		t.Error("a bad range should fail")
	}
}

func TestSplit(t *testing.T) {
	parts, err := open(t, 5).Split(2)
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) != 3 {
		t.Fatalf("got %d parts", len(parts))
	}
	want := [][]string{pages(1, 2), pages(3, 4), pages(5)}
	for i, p := range parts {
		if got := written(t, p); !equal(got, want[i]) {
			t.Errorf("part %d = %v, want %v", i, got, want[i])
		}
	}
	if _, err := open(t, 5).Split(0); err == nil {
		t.Error("splitting into nothing should fail")
	}
}

func TestSplitAt(t *testing.T) {
	parts, err := open(t, 6).SplitAt(3, 5)
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{pages(1, 2), pages(3, 4), pages(5, 6)}
	if len(parts) != len(want) {
		t.Fatalf("got %d parts", len(parts))
	}
	for i, p := range parts {
		if got := written(t, p); !equal(got, want[i]) {
			t.Errorf("part %d = %v, want %v", i, got, want[i])
		}
	}
	// Page one, and a repeat, name no new boundary.
	parts, err = open(t, 3).SplitAt(1, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) != 2 {
		t.Fatalf("got %d parts", len(parts))
	}
	if _, err := open(t, 3).SplitAt(9); err == nil {
		t.Error("a boundary past the end should fail")
	}
	// No boundaries at all is the whole document.
	parts, err = open(t, 3).SplitAt()
	if err != nil || len(parts) != 1 {
		t.Fatalf("got %d parts, %v", len(parts), err)
	}
}

func TestInfo(t *testing.T) {
	d := open(t, 1)
	if got, _ := reader.ToString(d.Info().Get("Title")); string(got) != "a title" {
		t.Errorf("Title = %q", got)
	}
	d.SetInfo("Title", "another")
	d.SetInfo("Author", "")
	d.SetInfo("Subject", "new")
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	back, err := Open(out)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := reader.ToString(back.Info().Get("Title")); string(got) != "another" {
		t.Errorf("Title = %q", got)
	}
	if back.Info().Get("Author").Kind() != reader.KindNull {
		t.Error("Author survived being cleared")
	}
	if got, _ := reader.ToString(back.Info().Get("Subject")); string(got) != "new" {
		t.Errorf("Subject = %q", got)
	}
	back.ClearInfo()
	out, err = back.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	bare, err := Open(out)
	if err != nil {
		t.Fatal(err)
	}
	if bare.Info() != nil {
		t.Errorf("the information dictionary survived: %v", bare.Info())
	}
}

func TestSetInfoOnADocumentWithout(t *testing.T) {
	d := New()
	d.SetInfo("Title", "x")
	if got, _ := reader.ToString(d.Info().Get("Title")); string(got) != "x" {
		t.Errorf("Title = %q", got)
	}
	d.SetVersion("1.4")
	if d.Version() != "1.4" {
		t.Errorf("version = %q", d.Version())
	}
}
