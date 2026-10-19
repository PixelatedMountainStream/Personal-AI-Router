// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// File-level import scan for local/scripts/check_fork_surface.py.
//
// Usage: importscan <file.go>...
// Prints one JSON object per file: {"file": "<as given>", "imports": ["a/b", ...]}.
// `go list` works per package, and fork glue shares `package main` with upstream
// files, so the surface check needs imports per file. Exit 1 on any parse error.
package main

import (
	"encoding/json"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"strconv"
)

func main() {
	enc := json.NewEncoder(os.Stdout)
	fset := token.NewFileSet()
	failed := false
	for _, path := range os.Args[1:] {
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			fmt.Fprintf(os.Stderr, "importscan: %s: %v\n", path, err)
			failed = true
			continue
		}
		imports := []string{}
		for _, spec := range f.Imports {
			if p, err := strconv.Unquote(spec.Path.Value); err == nil {
				imports = append(imports, p)
			}
		}
		if err := enc.Encode(map[string]any{"file": path, "imports": imports}); err != nil {
			fmt.Fprintf(os.Stderr, "importscan: %v\n", err)
			failed = true
		}
	}
	if failed {
		os.Exit(1)
	}
}
