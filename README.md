# hcl2json

This is a fork of [`hcl2json`](https://github.com/tmccombs/hcl2json), a tool for converting HCL files to JSON. This fork outputs an alternative JSON format that preserves AST information from blocks and expressions, so that downstream programs can provide richer analysis.

## Usage

```sh
# convert a file from hcl to json
$ hcl2json some-file.hcl > out.json
# reading from stdin also works
$ hcl2json < infile.hcl > out.json
# reading from multiple files will combine the results into a single JSON object, like hcl2json
# but will preserve source file references in the output JSON
$ hcl2json *.hcl > out.json
```

## Building

You can build and install `hcl2json` using `go install`. Since `hcl2json` uses Go modules, you will need to run this as
`go install github.com/keystone-whiteboards/hcl2json`.

Alternatively, you can clone and build the repository:

```
$ git clone https://github.com/keystone-whiteboards/hcl2json
$ cd hcl2json
$ go build
```

This will build an `hcl2json` executable in the directory.
