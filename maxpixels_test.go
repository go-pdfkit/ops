// Copyright (c) 2026, the go-pdfkit/ops authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package ops

import (
	"errors"
	"testing"

	"github.com/go-gfx/gfx/codec"
)

// picPixels is the size of the picture picBytes makes: 32 by 16.
const picPixels = 32 * 16

func TestThePictureCeilingIsInclusiveAndOneOverIsRefused(t *testing.T) {
	data := picBytes(t, codec.PNG, 255)

	at := New()
	at.SetMaxPixels(picPixels)
	if err := at.Picture(data, 0); err != nil {
		t.Fatalf("a picture exactly at the ceiling: %v", err)
	}

	over := New()
	over.SetMaxPixels(picPixels - 1)
	err := over.Picture(data, 0)
	if !errors.Is(err, codec.ErrTooLarge) {
		t.Fatalf("one pixel over the ceiling: got %v, want codec.ErrTooLarge", err)
	}
	if over.PageCount() != 0 {
		t.Fatal("a refused picture still made a page")
	}
}

func TestTheCeilingKnobRaisesTheDefaultAndZeroRestoresIt(t *testing.T) {
	data := picBytes(t, codec.PNG, 255)

	d := New()
	d.SetMaxPixels(picPixels - 1)
	if err := d.Picture(data, 0); !errors.Is(err, codec.ErrTooLarge) {
		t.Fatalf("below the picture: %v", err)
	}
	d.SetMaxPixels(picPixels * 4)
	if err := d.Picture(data, 0); err != nil {
		t.Fatalf("raised above the picture: %v", err)
	}
	d.SetMaxPixels(-5) // negative means the default, which is 40 Mpx
	if err := d.Picture(data, 0); err != nil {
		t.Fatalf("the default ceiling refused a 512 pixel picture: %v", err)
	}
}

func TestPlaceHonoursTheCeilingToo(t *testing.T) {
	data := picBytes(t, codec.PNG, 255)
	d := New()
	d.Blank(200, 200)
	d.SetMaxPixels(picPixels - 1)
	err := d.Place("1", Placement{Data: data})
	if !errors.Is(err, codec.ErrTooLarge) {
		t.Fatalf("Place over the ceiling: got %v, want codec.ErrTooLarge", err)
	}
	d.SetMaxPixels(picPixels)
	if err := d.Place("1", Placement{Data: data}); err != nil {
		t.Fatalf("Place at the ceiling: %v", err)
	}
}
