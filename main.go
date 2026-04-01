//go:build !(js && wasm)

package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"runtime/debug"

	"github.com/tmccombs/hcl2json/convert"
)

var version string = ""

const versionUsage = "Print the version of hcl2json"

func main() {
	logger := log.New(os.Stderr, "", 0)

	var options convert.Options
	var printVersion bool

	flag.BoolVar(&printVersion, "version", false, "Print the version of hcl2json")
	flag.BoolVar(&printVersion, "v", false, "Shorthand for -version")
	flag.Parse()

	if printVersion {
		if version == "" {
			//If version wasn't set at build time, try to descern it with
			// debug info
			if info, ok := debug.ReadBuildInfo(); ok {
				version = info.Main.Version
			}
			// If we still have an empty version, use a placeholder
			if version == "" {
				version = "devel"
			}
		}
		fmt.Println(version)
		os.Exit(0)
	}

	files := flag.Args()
	if len(files) == 0 {
		files = append(files, "-")
	}

	inputs := make([]convert.InputFile, 0, len(files))

	for _, inputFile := range files {
		filename := inputFile
		var stream io.Reader
		if filename == "-" {
			stream = os.Stdin
			filename = "STDIN" // for better error message
		} else {
			file, err := os.Open(filename)
			if err != nil {
				logger.Fatalf("Failed to open %s: %s\n", filename, err)
			}
			defer file.Close()
			stream = file
		}

		buffer := bytes.NewBuffer([]byte{})
		_, err := buffer.ReadFrom(stream)
		if err != nil {
			logger.Fatalf("Failed to read from %s: %s\n", filename, err)
		}
		buffer.WriteByte('\n') // just in case it doesn't have an ending newline

		inputs = append(inputs, convert.InputFile{Content: buffer.String(), Filename: filename})
	}

	converted, err := convert.Files(inputs, options)
	if err != nil {
		logger.Fatalf("Failed to convert files: %v", err)
	}

	var indented bytes.Buffer
	if err := json.Indent(&indented, converted, "", "    "); err != nil {
		logger.Fatalf("Failed to indent file: %v", err)
	}

	if _, err := indented.WriteTo(os.Stdout); err != nil {
		logger.Fatalf("Failed to write to standard out: %v", err)
	}
}
