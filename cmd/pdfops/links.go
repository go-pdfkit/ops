package main

import (
	"fmt"

	"github.com/go-pdfkit/ops"
)

// runLinks lists the links a document carries and where each one leads.
//
// ⛔ A link that leads NOWHERE THIS CAN FOLLOW is printed too, as "nowhere".
// Printing only the ones that work would have a reader counting links of a file
// they are checking get a smaller number than the file holds, which is the one
// thing a listing must not do.
func runLinks(c *context, args []string) error {
	fs := flags("links")
	spec := fs.String("pages", "all", "which pages to list the links of")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := wantArgs(fs, 1, "[-pages <range>] <in.pdf>"); err != nil {
		return err
	}
	d, err := c.open(fs.Arg(0))
	if err != nil {
		return err
	}
	wanted, err := ops.ParseRange(*spec, d.PageCount())
	if err != nil {
		return err
	}
	on := map[int]bool{}
	for _, p := range wanted {
		on[p] = true
	}

	shown := 0
	for _, l := range d.Links() {
		if !on[l.Page] {
			continue
		}
		shown++
		fmt.Fprintf(c.out, "%d\t%s\t%s\n", l.Page, rect(l.Rect), leads(l))
	}
	if shown == 0 {
		fmt.Fprintln(c.out, "the file carries no links")
	}
	return nil
}

// leads says where one link goes, in the words a person reads.
func leads(l ops.Link) string {
	switch {
	case l.URI != "":
		return l.URI
	case l.Target != 0:
		return fmt.Sprintf("page %d", l.Target)
	}
	return "nowhere"
}

// rect prints a rectangle as the four numbers a reader can compare with the
// page's own, without the trailing zeros a float carries.
func rect(r [4]float64) string {
	return fmt.Sprintf("%g,%g,%g,%g", r[0], r[1], r[2], r[3])
}
