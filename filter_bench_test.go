package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/nicad/autoversion/filterrules"
	"golang.org/x/sys/unix"
)

// benchTree is a generated source: depth 4, about 2 000 folders and 40 000
// files, CACHEDIR.TAG in every 20th folder, the Time Machine attribute on
// every 50th folder and every 200th file (macOS only). Built once per run.
var benchTree struct {
	once    sync.Once
	root    string
	folders int
	files   int
}

const tmAttr = "com.apple.metadata:com_apple_backup_excludeItem"

func benchRoot(b *testing.B) string {
	benchTree.once.Do(func() {
		root, err := os.MkdirTemp("", "autoversion-bench")
		if err != nil {
			b.Fatal(err)
		}
		root, _ = filepath.EvalSymlinks(root)
		benchTree.root = root
		var build func(dir string, depth int)
		build = func(dir string, depth int) {
			if depth == 4 {
				return
			}
			for i := range 7 {
				sub := filepath.Join(dir, fmt.Sprintf("d%d", i))
				if err := os.Mkdir(sub, 0o755); err != nil {
					b.Fatal(err)
				}
				benchTree.folders++
				if benchTree.folders%20 == 0 {
					os.WriteFile(filepath.Join(sub, "CACHEDIR.TAG"), []byte("Signature: 8a477f597d28d172789f06886806bc55"), 0o644)
				}
				if runtime.GOOS == "darwin" && benchTree.folders%50 == 0 {
					unix.Setxattr(sub, tmAttr, []byte("com.apple.backupd"), 0)
				}
				for j := range 20 {
					name := filepath.Join(sub, fmt.Sprintf("f%d.txt", j))
					if j%5 == 0 {
						name = filepath.Join(sub, fmt.Sprintf("f%d.log", j))
					}
					if err := os.WriteFile(name, []byte("x"), 0o644); err != nil {
						b.Fatal(err)
					}
					benchTree.files++
					if runtime.GOOS == "darwin" && benchTree.files%200 == 0 {
						unix.Setxattr(name, tmAttr, []byte("com.apple.backupd"), 0)
					}
				}
				build(sub, depth+1)
			}
		}
		build(root, 0)
	})
	return benchTree.root
}

// BenchmarkWalk walks the generated tree with three rule sets and reports,
// besides time and allocations, the disk lookups (lstat, lgetxattr) the
// marker and attribute rules asked for, per walk.
func BenchmarkWalk(b *testing.B) {
	root := benchRoot(b)
	sets := []struct{ name, rules string }{
		{"default", "use default\n"},
		{"markers", "use default\nuse cache-markers\n"},
		{"markers+xattr", "use default\nuse cache-markers\nuse time-machine\n"},
	}
	for _, set := range sets {
		b.Run(set.name, func(b *testing.B) {
			_, rules, err := parseRules("version 1\n" + set.rules)
			if err != nil {
				b.Fatal(err)
			}
			s := newSourceRules(root, rules, false)
			kept := 0
			filterrules.LookupCount.Store(0)
			b.ReportAllocs()
			for b.Loop() {
				kept = 0
				s.walk(root, func(path string, info fs.FileInfo) error {
					kept++
					return nil
				})
			}
			b.ReportMetric(float64(filterrules.LookupCount.Load())/float64(b.N), "lookups/op")
			b.ReportMetric(float64(kept), "kept")
		})
	}
}
