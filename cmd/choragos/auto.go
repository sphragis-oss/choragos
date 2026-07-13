// SPDX-License-Identifier: Apache-2.0

package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// autoScanCap bounds the file walk so a huge repo cannot stall init --auto.
const autoScanCap = 20000

// language ties a project manifest to the source extensions that measure dominance.
type language struct {
	name     string
	manifest []string
	exts     []string
}

// autoLanguages is the detection table, in template name order.
var autoLanguages = []language{
	{"go", []string{"go.mod"}, []string{".go"}},
	{"node", []string{"package.json"}, []string{".js", ".jsx", ".ts", ".tsx", ".mjs", ".cjs"}},
	{"python", []string{"pyproject.toml", "setup.py", "requirements.txt"}, []string{".py"}},
	{"rust", []string{"Cargo.toml"}, []string{".rs"}},
}

// skippedDirs are trees that would skew the source count or take forever to walk.
var skippedDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true, "target": true,
	"dist": true, "build": true, ".venv": true, "venv": true, "__pycache__": true,
}

// detectProject returns the dominant detected language and any others, or "" when none match.
func detectProject(dir string) (dominant string, others []string) {
	found := map[string]bool{}
	for _, l := range autoLanguages {
		for _, m := range l.manifest {
			if _, err := os.Stat(filepath.Join(dir, m)); err == nil {
				found[l.name] = true
				break
			}
		}
	}
	if len(found) == 0 {
		return "", nil
	}
	counts := countSources(dir, found)
	best, bestN := "", -1
	for _, l := range autoLanguages { // table order breaks ties deterministically
		if found[l.name] && counts[l.name] > bestN {
			best, bestN = l.name, counts[l.name]
		}
	}
	for name := range found {
		if name != best {
			others = append(others, name)
		}
	}
	sort.Strings(others)
	return best, others
}

// countSources counts source files per detected language, capped and skipping dependency trees.
func countSources(dir string, found map[string]bool) map[string]int {
	extLang := map[string]string{}
	for _, l := range autoLanguages {
		if !found[l.name] {
			continue
		}
		for _, e := range l.exts {
			extLang[e] = l.name
		}
	}
	counts := map[string]int{}
	visited := 0
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable entries just don't count
		}
		if d.IsDir() {
			if skippedDirs[d.Name()] || (strings.HasPrefix(d.Name(), ".") && path != dir) {
				return filepath.SkipDir
			}
			return nil
		}
		if visited++; visited > autoScanCap {
			return filepath.SkipAll
		}
		if l, ok := extLang[filepath.Ext(d.Name())]; ok {
			counts[l]++
		}
		return nil
	})
	return counts
}
