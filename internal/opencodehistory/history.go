// SPEC: _spec/defs/opencode/engine-local-history.puml
package opencodehistory

import (
	"archive/tar"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

const (
	Commit     = "commit"
	Exists     = 3
	Corrupt    = 4
	Incomplete = 5
	Absent     = 3
)

func Run(mode string, in io.Reader, out, stderr io.Writer, env func(string) string) int {
	db := env("OPENCODE_DB")
	if db == "" {
		data := env("XDG_DATA_HOME")
		if data == "" {
			data = filepath.Join(env("HOME"), ".local", "share")
		}
		db = filepath.Join(data, "opencode", "opencode.db")
	}
	var code int
	var err error
	switch mode {
	case "ready":
		err = ready(db)
	case "import":
		code, err = importTo(in, db, env("TMPDIR"))
	case "export":
		code, err = exportFrom(out, db, env("TMPDIR"))
	default:
		fmt.Fprintln(stderr, "usage: proveo-entrypoint opencode-history import|export|ready")
		return 2
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return code
}

func ready(db string) error {
	root := filepath.Dir(db)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(root, ".proveo-history-ready"), os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	return f.Close()
}

func importTo(in io.Reader, dest, tmp string) (int, error) {
	if _, err := os.Lstat(dest); err == nil {
		return Exists, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return 0, err
	}
	stage, err := os.MkdirTemp(tmp, "proveo-import-")
	if err != nil {
		return 0, err
	}
	defer func() { _ = os.RemoveAll(stage) }()
	ok, err := receive(tar.NewReader(in), stage)
	if err != nil {
		return 0, err
	}
	if !ok {
		return Incomplete, nil
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return 0, err
	}
	f, err := os.CreateTemp(filepath.Dir(dest), ".proveo-import-")
	if err != nil {
		return 0, err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if err := f.Close(); err != nil {
		return 0, err
	}
	code, err := backup(filepath.Join(stage, "opencode.db"), f.Name(), true)
	if err != nil || code != 0 {
		return code, err
	}
	if err := os.Link(f.Name(), dest); errors.Is(err, os.ErrExist) {
		return Exists, nil
	} else {
		return 0, err
	}
}

func receive(r *tar.Reader, stage string) (bool, error) {
	seen := map[string]bool{}
	for {
		h, err := r.Next()
		if err != nil {
			return false, nil
		}
		if h.Typeflag != tar.TypeReg || seen[h.Name] {
			return false, nil
		}
		if h.Name == Commit {
			return seen["opencode.db"] && h.Size == 0, nil
		}
		switch h.Name {
		case "opencode.db", "opencode.db-wal", "opencode.db-journal":
		default:
			return false, nil
		}
		f, err := os.OpenFile(filepath.Join(stage, h.Name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return false, err
		}
		_, copyErr := io.Copy(f, r)
		closeErr := f.Close()
		if errors.Is(copyErr, io.ErrUnexpectedEOF) {
			return false, closeErr
		}
		if err := errors.Join(copyErr, closeErr); err != nil {
			return false, err
		}
		seen[h.Name] = true
	}
}

func exportFrom(out io.Writer, source, tmp string) (int, error) {
	info, err := os.Stat(source)
	if errors.Is(err, os.ErrNotExist) || (err == nil && !info.Mode().IsRegular()) {
		return Absent, nil
	}
	if err != nil {
		return 0, err
	}
	stage, err := os.MkdirTemp(tmp, "proveo-export-")
	if err != nil {
		return 0, err
	}
	defer func() { _ = os.RemoveAll(stage) }()
	dest := filepath.Join(stage, "opencode.db")
	if code, err := backup(source, dest, false); err != nil || code != 0 {
		return code, err
	}
	f, err := os.Open(dest)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	_, err = io.Copy(out, f)
	return 0, err
}

const sqliteBackup = `
import { DatabaseSync, backup } from "node:sqlite";
const [source, dest, check] = process.argv.slice(1);
const db = new DatabaseSync(source);
db.exec("PRAGMA busy_timeout=30000");
if (check === "true") {
  try {
    if (db.prepare("PRAGMA quick_check").get().quick_check !== "ok") process.exit(4);
  } catch (error) {
    if (error.code === "ERR_SQLITE_ERROR") process.exit(4);
    throw error;
  }
}
await backup(db, dest);
db.close();
const copy = new DatabaseSync(dest);
copy.exec("PRAGMA journal_mode=DELETE");
copy.close();
`

func backup(source, dest string, check bool) (int, error) {
	cmd := exec.Command("node", "--no-warnings", "--input-type=module", "-e", sqliteBackup, source, dest, fmt.Sprint(check))
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if check && errors.As(err, &exit) && exit.ExitCode() == Corrupt {
		return Corrupt, nil
	}
	if err != nil {
		return 0, fmt.Errorf("SQLite backup failed: %w: %s", err, out)
	}
	return 0, nil
}
