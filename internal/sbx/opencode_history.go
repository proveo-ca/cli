// SPEC: _spec/defs/opencode/engine-local-history.puml
package sbx

import (
	"archive/tar"
	"errors"
	"io"
	"io/fs"
	"maps"
	"os"

	"github.com/proveo-ca/proveo/internal/opencodehistory"
)

// OpenCodeHistoryGateEnv defers credential startup until history import completes.
const OpenCodeHistoryGateEnv = "PROVEO_OPENCODE_HISTORY_IMPORT=1"

// OpenCodeDatabaseHeader opens every SQLite database file.
const OpenCodeDatabaseHeader = "SQLite format 3\x00"

// OpenCodeImportCommit is the last member of an import stream; the import installs nothing without it.
const OpenCodeImportCommit = opencodehistory.Commit

const (
	OpenCodeImportExisting   = opencodehistory.Exists
	OpenCodeImportCorrupt    = opencodehistory.Corrupt
	OpenCodeImportIncomplete = opencodehistory.Incomplete
	OpenCodeExportNone       = opencodehistory.Absent
)

// ErrOpenCodeImportChanged reports a host database file that changed while it streamed.
var ErrOpenCodeImportChanged = errors.New("changed while it was copied")

// openCodeImportSides are the suffixes of the host database files an import carries.
var openCodeImportSides = []string{"", "-wal", "-journal"}

// OpenCodeImportFiles stats src and its -wal/-journal, keyed by suffix; -shm never travels.
func OpenCodeImportFiles(src string) map[string]os.FileInfo {
	files := map[string]os.FileInfo{}
	for _, s := range openCodeImportSides {
		if info, err := os.Lstat(src + s); err == nil && info.Mode().IsRegular() {
			files[s] = info
		}
	}
	return files
}

// WriteOpenCodeImport streams files, OpenCodeImportFiles(src) taken before, to w as the tar
// the engine importer reads; it appends OpenCodeImportCommit only when none changed while it streamed,
// else it returns ErrOpenCodeImportChanged.
func WriteOpenCodeImport(w io.Writer, src string, files map[string]os.FileInfo) error {
	tw := tar.NewWriter(w)
	for _, s := range openCodeImportSides {
		info, ok := files[s]
		if !ok {
			continue
		}
		if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: "opencode.db" + s, Mode: 0o600, Size: info.Size()}); err != nil {
			return err
		}
		if err := copyOpenCodeFile(tw, src+s, info.Size()); err != nil {
			return err
		}
	}
	if !maps.EqualFunc(OpenCodeImportFiles(src), files, sameOpenCodeFile) {
		return ErrOpenCodeImportChanged
	}
	if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: OpenCodeImportCommit, Mode: 0o600}); err != nil {
		return err
	}
	return tw.Close()
}

// copyOpenCodeFile copies the first size bytes of path to w.
func copyOpenCodeFile(w io.Writer, path string, size int64) error {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return ErrOpenCodeImportChanged
	}
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if _, err := io.CopyN(w, f, size); errors.Is(err, io.EOF) {
		return ErrOpenCodeImportChanged
	} else if err != nil {
		return err
	}
	return nil
}

// sameOpenCodeFile reports an unchanged size and mtime.
func sameOpenCodeFile(a, b os.FileInfo) bool {
	return a.Size() == b.Size() && a.ModTime().Equal(b.ModTime())
}

// OpenCodeImportArgs runs the import script in engine name with stdin attached.
func OpenCodeImportArgs(name string) []string {
	return []string{"exec", "-i", "-w", "/", name, "--", "proveo-entrypoint", "opencode-history", "import"}
}

// OpenCodeExportArgs streams engine name's OpenCode database on stdout.
func OpenCodeExportArgs(name string) []string {
	return []string{"exec", "-w", "/", name, "--", "proveo-entrypoint", "opencode-history", "export"}
}

// OpenCodeHistoryReadyArgs enables credential startup in the imported engine.
func OpenCodeHistoryReadyArgs(name string) []string {
	return []string{"exec", "-w", "/", name, "--", "proveo-entrypoint", "opencode-history", "ready"}
}
