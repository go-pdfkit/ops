// Copyright (c) 2026, the go-pdfkit/ops authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

//go:build unix

package main

import (
	"os"
	"syscall"
)

// openNoBlock opens a file for reading without waiting: opening a FIFO with
// O_NONBLOCK returns at once instead of waiting for a writer, so a path swapped
// for a FIFO after it was looked at cannot hang the verb. The caller still has
// to look at what it opened.
func openNoBlock(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}
