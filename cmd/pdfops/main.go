// Command pdfops does to a PDF the things people actually want done to one:
// merge, extract, delete, reorder, rotate, crop, split, and read or strip the
// metadata. Everything happens in this process; nothing is uploaded anywhere.
package main

import (
	"os"
)

// osExit is a variable so the tests can reach the exit path without ending the
// test binary.
var osExit = os.Exit

func main() { osExit(run(os.Args[1:], os.Stdout, os.Stderr)) }
