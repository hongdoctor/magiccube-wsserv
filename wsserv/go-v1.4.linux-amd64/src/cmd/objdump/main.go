// Copyright 2012 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Objdump disassembles executable files.
//
// Usage:
//
//	go tool objdump [-s symregexp] binary
//
// Objdump prints a disassembly of all text symbols (code) in the binary.
// If the -s option is present, objdump only disassembles
// symbols with names matching the regular expression.
//
// Alternate usage:
//
//	go tool objdump binary start end
//
// In this mode, objdump disassembles the binary starting at the start address and
// stopping at the end address. The start and end addresses are program
// counters written in hexadecimal with optional leading 0x prefix.
// In this mode, objdump prints a sequence of stanzas of the form:
//
//	file:line
//	 address: assembly
//	 address: assembly
//	 ...
//
// Each stanza gives the disassembly for a contiguous range of addresses
// all mapped to the same original source file and line number.
// This mode is intended for use by pprof.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"regexp"
	"strconv"
	"strings"

	"cmd/internal/objfile"
)

var symregexp = flag.String("s", "", "only dump symbols matching this regexp")
var symRE *regexp.Regexp

func usage() {
	fmt.Fprintf(os.Stderr, "usage: go tool objdump [-s symregexp] binary [start end]\n\n")
	flag.PrintDefaults()
	os.Exit(2)
}

type lookupFunc func(addr uint64) (sym string, base uint64)
type disasmFunc func(code []byte, pc uint64, lookup lookupFunc) (text string, size int)

func main() {
	log.SetFlags(0)
	log.SetPrefix("objdump: ")

	flag.Usage = usage
	flag.Parse()
	if flag.NArg() != 1 && flag.NArg() != 3 {
		usage()
	}

	if *symregexp != "" {
		re, err := regexp.Compile(*symregexp)
		if err != nil {
			log.Fatalf("invalid -s regexp: %v", err)
		}
		symRE = re
	}

	f, err := objfile.Open(flag.Arg(0))
	if err != nil {
		log.Fatal(err)
	}

	dis, err := f.Disasm()
	if err != nil {
		log.Fatal("disassemble %s: %v", flag.Arg(0), err)
	}

	switch flag.NArg() {
	default:
		usage()
	case 1:
		// disassembly of entire object
		dis.Print(os.Stdout, symRE, 0, ^uint64(0))
		os.Exit(0)

	case 3:
		// disassembly of PC range
		start, err := strconv.ParseUint(strings.TrimPrefix(flag.Arg(1), "0x"), 16, 64)
		if err != nil {
			log.Fatalf("invalid start PC: %v", err)
		}
		end, err := strconv.ParseUint(strings.TrimPrefix(flag.Arg(2), "0x"), 16, 64)
		if err != nil {
			log.Fatalf("invalid end PC: %v", err)
		}
		dis.Print(os.Stdout, symRE, start, end)
		os.Exit(0)
	}
}
