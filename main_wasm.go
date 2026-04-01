//go:build js && wasm

package main

import (
	"bytes"
	"encoding/json"
	"syscall/js"

	"github.com/tmccombs/hcl2json/convert"
)

// hcl2jsonConvert is exposed to JavaScript as globalThis.hcl2jsonConvert.
//
// It accepts a single argument: a JSON string encoding an array of
// {filename: string, content: string} objects — the HCL files to convert.
//
// It returns a JSON string of the merged HCL→JSON output, or throws on error.
func hcl2jsonConvert(this js.Value, args []js.Value) any {
	if len(args) < 1 {
		return map[string]any{"error": "hcl2jsonConvert requires one argument (JSON string of files)"}
	}

	inputJSON := args[0].String()

	type fileInput struct {
		Filename string `json:"filename"`
		Content  string `json:"content"`
	}

	var files []fileInput
	if err := json.Unmarshal([]byte(inputJSON), &files); err != nil {
		return map[string]any{"error": "failed to parse input JSON: " + err.Error()}
	}

	inputs := make([]convert.InputFile, 0, len(files))
	for _, f := range files {
		inputs = append(inputs, convert.InputFile{
			Filename: f.Filename,
			Bytes:    []byte(f.Content),
		})
	}

	converted, err := convert.Files(inputs, convert.Options{})
	if err != nil {
		return map[string]any{"error": "hcl2json conversion failed: " + err.Error()}
	}

	var indented bytes.Buffer
	if err := json.Indent(&indented, converted, "", "    "); err != nil {
		return map[string]any{"error": "failed to indent JSON: " + err.Error()}
	}

	return indented.String()
}

func main() {
	js.Global().Set("hcl2jsonConvert", js.FuncOf(hcl2jsonConvert))

	// Block forever so the WASM instance stays alive.
	select {}
}
