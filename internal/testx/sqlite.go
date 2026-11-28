// SPEC: _spec/defs/opencode/engine-local-history.puml
package testx

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

const WALDatabase = `
const { DatabaseSync } = require("node:sqlite");
const db = new DatabaseSync(process.argv[1]);
db.exec("PRAGMA journal_mode=WAL; PRAGMA wal_autocheckpoint=0; CREATE TABLE session (id TEXT)");
db.prepare("INSERT INTO session VALUES (?)").run(process.argv[2]);
process.exit(0);
`

func SQLiteNode(t testing.TB) string {
	t.Helper()
	path, err := exec.LookPath("node")
	if err != nil || exec.Command(path, "--no-warnings", "-e", "require('node:sqlite')").Run() != nil {
		t.Skip("Node with SQLite unavailable")
	}
	return path
}

func WriteWALDatabase(t testing.TB, path, id string) {
	t.Helper()
	if out, err := exec.Command(SQLiteNode(t), "--no-warnings", "-e", WALDatabase, path, id).CombinedOutput(); err != nil {
		t.Fatalf("seed %s: %v\n%s", path, err, out)
	}
	if _, err := os.Stat(path + "-wal"); err != nil {
		t.Fatalf("seed %s WAL: %v", path, err)
	}
}

func SQLiteSessions(t testing.TB, path string) string {
	t.Helper()
	out, err := exec.Command(SQLiteNode(t), "--no-warnings", "-e",
		`const db = new (require("node:sqlite").DatabaseSync)(process.argv[1]);
console.log(db.prepare("SELECT id FROM session ORDER BY id").all().map(row => row.id).join(","));`, path).CombinedOutput()
	if err != nil {
		t.Fatalf("read %s: %v\n%s", path, err, out)
	}
	return strings.TrimSpace(string(out))
}
