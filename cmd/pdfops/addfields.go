package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"

	"github.com/go-pdfkit/forms"
)

// The description a person writes to create a form. It is the shape of
// forms.Field with the parts a file cannot carry left out, named as JSON
// rather than as Go so that the file reads like what it describes.
type fieldFile struct {
	Name      string        `json:"name"`
	Kind      string        `json:"kind"`
	Value     string        `json:"value,omitempty"`
	Values    []string      `json:"values,omitempty"`
	Options   []optionFile  `json:"options,omitempty"`
	MaxLen    int           `json:"maxLen,omitempty"`
	Quadding  int           `json:"align,omitempty"`
	ReadOnly  bool          `json:"readOnly,omitempty"`
	Required  bool          `json:"required,omitempty"`
	NoExport  bool          `json:"noExport,omitempty"`
	Multiline bool          `json:"multiline,omitempty"`
	Password  bool          `json:"password,omitempty"`
	Comb      bool          `json:"comb,omitempty"`
	Sorted    bool          `json:"sorted,omitempty"`
	MultiSel  bool          `json:"multiSelect,omitempty"`
	Editable  bool          `json:"editable,omitempty"`
	Widgets   []widgetFile  `json:"widgets"`
}

type optionFile struct {
	Value string `json:"value"`
	Label string `json:"label,omitempty"`
}

type widgetFile struct {
	Page   int        `json:"page"`
	Rect   [4]float64 `json:"rect"`
	On     string     `json:"on,omitempty"`
	Hidden bool       `json:"hidden,omitempty"`
}

// kinds maps what a person writes to what the format calls it. The names are
// the ones Kind.String prints, so a description can be written from a listing.
var kinds = map[string]forms.Kind{
	"text":      forms.Text,
	"checkbox":  forms.Checkbox,
	"radio":     forms.Radio,
	"button":    forms.PushButton,
	"combo":     forms.ComboBox,
	"list":      forms.ListBox,
	"signature": forms.Signature,
}

// ⛔ BOUNDS ON A DESCRIPTION FILE. The input is a file somebody hands this, and
// a list is as long as the file says: ten million fields, or a choice field
// with ten million rows, is a small JSON file that asks for an enormous PDF
// and all the memory to build it. Both are larger than any real form by orders
// of magnitude.
const (
	maxFields  = 10000
	maxOptions = 10000
	maxWidgets = 1000
)

// runAddFields puts new fields into a document from a description.
//
// ⛔ It is the other half of `fill`, which can only change a field the document
// already has.
func runAddFields(c *context, args []string) error {
	fs := flags("addfields")
	desc := fs.String("fields", "", "a JSON file describing the fields to add")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := wantArgs(fs, 2, "<in.pdf> <out.pdf>"); err != nil {
		return err
	}
	if *desc == "" {
		return fmt.Errorf("nothing to add: give -fields <description.json>")
	}
	fields, err := readFieldFile(*desc)
	if err != nil {
		return err
	}
	// c.open and save are the ones every other verb uses: the password, the
	// write and their error paths in one place rather than in fifty-four.
	d, err := c.open(fs.Arg(0))
	if err != nil {
		return err
	}
	if err := d.AddFields(fields...); err != nil {
		return err
	}
	if err := save(d, fs.Arg(1)); err != nil {
		return err
	}
	fmt.Fprintf(c.out, "added %d field(s)\n", len(fields))
	return nil
}

func readFieldFile(path string) ([]forms.Field, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var in []fieldFile
	// ⛔ DisallowUnknownFields: a description with "multiline" spelled wrong is
	// a form missing the thing it was written to ask for, and a parser that
	// ignores what it does not know says nothing about it.
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	if len(in) > maxFields {
		return nil, fmt.Errorf("%s describes %d fields; the most this adds at once is %d", path, len(in), maxFields)
	}
	out := make([]forms.Field, 0, len(in))
	for i, f := range in {
		kind, ok := kinds[f.Kind]
		if !ok {
			return nil, fmt.Errorf("field %d (%q) is of kind %q; the kinds are text, checkbox, radio, button, combo, list and signature",
				i+1, f.Name, f.Kind)
		}
		if len(f.Options) > maxOptions {
			return nil, fmt.Errorf("field %q has %d options; the most is %d", f.Name, len(f.Options), maxOptions)
		}
		if len(f.Widgets) > maxWidgets {
			return nil, fmt.Errorf("field %q shows in %d places; the most is %d", f.Name, len(f.Widgets), maxWidgets)
		}
		fld := forms.Field{
			Name: f.Name, Kind: kind, Value: f.Value, Values: f.Values,
			MaxLen: f.MaxLen, Quadding: f.Quadding,
			ReadOnly: f.ReadOnly, Required: f.Required, NoExport: f.NoExport,
			Multiline: f.Multiline, Password: f.Password, Comb: f.Comb,
			Sorted: f.Sorted, MultiSelect: f.MultiSel, Editable: f.Editable,
		}
		for _, o := range f.Options {
			fld.Options = append(fld.Options, forms.Option{Value: o.Value, Label: o.Label})
		}
		for _, wdg := range f.Widgets {
			fld.Widgets = append(fld.Widgets, forms.Widget{
				Page: wdg.Page, Rect: wdg.Rect, On: wdg.On, Hidden: wdg.Hidden,
			})
		}
		out = append(out, fld)
	}
	return out, nil
}
