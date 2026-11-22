// Copyright (c) 2026, the go-pdfkit/ops authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

//go:build !unix

package main

import "os"

// openNoBlock opens a file for reading; there is no O_NONBLOCK here, and no
// FIFO to wait on.
func openNoBlock(path string) (*os.File, error) { return os.Open(path) }
