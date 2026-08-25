package ops

import (
	"fmt"
	"sort"
)

// Append adds the pages of another document to the end of this one. The other
// document is not changed, and the two may come from different files.
func (d *Doc) Append(other *Doc) {
	d.pages = append(d.pages, other.pages...)
	if d.version < other.version {
		d.version = other.version
	}
	if d.info == nil && other.info != nil {
		d.info = other.info
	}
}

// Merge joins documents in the order given.
func Merge(docs ...*Doc) *Doc {
	out := New()
	for _, d := range docs {
		out.Append(d)
	}
	return out
}

// Select keeps only the pages the range names, in the order it names them, so
// it extracts, reorders and duplicates in one verb.
func (d *Doc) Select(spec string) error {
	nums, err := ParseRange(spec, len(d.pages))
	if err != nil {
		return err
	}
	pages := make([]Page, 0, len(nums))
	for _, n := range nums {
		pages = append(pages, d.pages[n-1])
	}
	d.pages = pages
	return nil
}

// Delete removes the pages the range names.
func (d *Doc) Delete(spec string) error {
	nums, err := ParseRange(spec, len(d.pages))
	if err != nil {
		return err
	}
	drop := map[int]bool{}
	for _, n := range nums {
		drop[n] = true
	}
	pages := make([]Page, 0, len(d.pages))
	for i, p := range d.pages {
		if !drop[i+1] {
			pages = append(pages, p)
		}
	}
	d.pages = pages
	return nil
}

// Reverse puts the pages in the opposite order.
func (d *Doc) Reverse() {
	for i, j := 0, len(d.pages)-1; i < j; i, j = i+1, j-1 {
		d.pages[i], d.pages[j] = d.pages[j], d.pages[i]
	}
}

// Move takes the page at from and puts it at to, both counting from one, the
// other pages closing up behind it.
func (d *Doc) Move(from, to int) error {
	if err := d.check(from); err != nil {
		return err
	}
	if err := d.check(to); err != nil {
		return err
	}
	p := d.pages[from-1]
	rest := append(d.pages[:from-1:from-1], d.pages[from:]...)
	out := make([]Page, 0, len(d.pages))
	out = append(out, rest[:to-1]...)
	out = append(out, p)
	out = append(out, rest[to-1:]...)
	d.pages = out
	return nil
}

// Rotate turns the pages the range names by the given number of degrees, which
// must be a multiple of ninety. The rotation is relative to what the page
// already had, so rotating twice by ninety turns a page upside down.
func (d *Doc) Rotate(spec string, degrees int) error {
	if degrees%90 != 0 {
		return fmt.Errorf("ops: %d degrees is not a multiple of ninety", degrees)
	}
	nums, err := ParseRange(spec, len(d.pages))
	if err != nil {
		return err
	}
	for _, n := range nums {
		d.pages[n-1].rotate = normaliseRotation(d.pages[n-1].rotate + degrees)
	}
	return nil
}

// SetRotation turns the pages the range names to an absolute angle.
func (d *Doc) SetRotation(spec string, degrees int) error {
	if degrees%90 != 0 {
		return fmt.Errorf("ops: %d degrees is not a multiple of ninety", degrees)
	}
	nums, err := ParseRange(spec, len(d.pages))
	if err != nil {
		return err
	}
	for _, n := range nums {
		d.pages[n-1].rotate = normaliseRotation(degrees)
	}
	return nil
}

// Crop sets the visible area of the pages the range names, in points, as
// [left bottom right top]. It sets the crop box; the media box, which is the
// paper the page is on, is left alone.
func (d *Doc) Crop(spec string, box [4]float64) error {
	if box[0] >= box[2] || box[1] >= box[3] {
		return fmt.Errorf("ops: [%g %g %g %g] is not a box", box[0], box[1], box[2], box[3])
	}
	nums, err := ParseRange(spec, len(d.pages))
	if err != nil {
		return err
	}
	for _, n := range nums {
		d.pages[n-1].crop = []float64{box[0], box[1], box[2], box[3]}
	}
	return nil
}

// Resize sets the media box — the paper — of the pages the range names.
func (d *Doc) Resize(spec string, box [4]float64) error {
	if box[0] >= box[2] || box[1] >= box[3] {
		return fmt.Errorf("ops: [%g %g %g %g] is not a box", box[0], box[1], box[2], box[3])
	}
	nums, err := ParseRange(spec, len(d.pages))
	if err != nil {
		return err
	}
	for _, n := range nums {
		d.pages[n-1].media = []float64{box[0], box[1], box[2], box[3]}
	}
	return nil
}

// Split cuts the document into pieces of at most n pages each.
func (d *Doc) Split(n int) ([]*Doc, error) {
	if n < 1 {
		return nil, fmt.Errorf("ops: a split of %d pages makes no sense", n)
	}
	var out []*Doc
	for i := 0; i < len(d.pages); i += n {
		end := i + n
		if end > len(d.pages) {
			end = len(d.pages)
		}
		out = append(out, d.slice(i, end))
	}
	return out, nil
}

// SplitAt cuts the document before each of the given page numbers, which is
// how a report is broken into chapters.
func (d *Doc) SplitAt(at ...int) ([]*Doc, error) {
	bounds := []int{0}
	seen := map[int]bool{}
	for _, n := range at {
		if err := d.check(n); err != nil {
			return nil, err
		}
		if n > 1 && !seen[n] {
			seen[n] = true
			bounds = append(bounds, n-1)
		}
	}
	sort.Ints(bounds)
	bounds = append(bounds, len(d.pages))
	var out []*Doc
	for i := 0; i+1 < len(bounds); i++ {
		out = append(out, d.slice(bounds[i], bounds[i+1]))
	}
	return out, nil
}

// slice returns a document over the half-open range of pages.
func (d *Doc) slice(from, to int) *Doc {
	out := &Doc{version: d.version, info: d.info}
	out.pages = append(out.pages, d.pages[from:to]...)
	return out
}
