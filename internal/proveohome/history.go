// SPEC: _spec/internal/sbx/state-sync.puml
package proveohome

import (
	"bufio"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var historyID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,127}$`)

type Checkpoint struct {
	Attempt, NativeID, Target, File, SHA256, GitTip string
	Observed                                        time.Time
}

type HistoryStatus struct {
	Attempt, State, Detail string
}

func HistorySupported(target string) bool {
	switch harnessFamily(target) {
	case "codex", "claudecode":
		return true
	}
	return false
}

func RecordHistory(dir string, status HistoryStatus) error {
	if !historyID.MatchString(status.Attempt) {
		return fmt.Errorf("invalid history attempt %q", status.Attempt)
	}
	return writeHistoryJSON(filepath.Join(dir, "outcomes", status.Attempt+".json"), status)
}

func CheckpointSession(dir, home, target, cwd, attempt, gitTip string, since time.Time) (Checkpoint, error) {
	if !historyID.MatchString(attempt) {
		return Checkpoint{}, fmt.Errorf("invalid history attempt %q", attempt)
	}
	if !HistorySupported(target) {
		return Checkpoint{}, RecordHistory(dir, HistoryStatus{attempt, "unavailable", "portable native history is unavailable for " + target})
	}
	if home == "" {
		return Checkpoint{}, fmt.Errorf("missing mounted agent home for %s", target)
	}
	if previous, err := readCheckpoint(dir, attempt); err == nil {
		return previous, publishHistoryHead(dir, previous)
	} else if !errors.Is(err, os.ErrNotExist) {
		return Checkpoint{}, err
	}
	c, err := findSession(home, target, cwd, since)
	if err != nil {
		return Checkpoint{}, err
	}
	if c.NativeID == "" {
		return Checkpoint{}, RecordHistory(dir, HistoryStatus{attempt, "no-session", ""})
	}
	if strings.Contains(attempt, "-checkpoint-") {
		if head, err := FindCheckpoint(dir, ""); err == nil &&
			head.NativeID == c.NativeID && !head.Observed.Before(c.Observed) {
			return head, nil
		}
	}
	c.Attempt, c.Target, c.GitTip = attempt, target, gitTip
	if err := c.publish(dir, home); err != nil {
		return Checkpoint{}, err
	}
	return c, publishHistoryHead(dir, c)
}

func publishHistoryHead(dir string, c Checkpoint) error {
	head, err := FindCheckpoint(dir, "")
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	newer := head.Observed.Before(c.Observed)
	if head.Observed.Equal(c.Observed) {
		newer = (c.GitTip != "" && head.GitTip == "") ||
			(c.GitTip == head.GitTip && head.Attempt < c.Attempt)
	}
	if newer {
		if err := writeHistoryJSON(filepath.Join(dir, "head.json"), c); err != nil {
			return err
		}
	}
	return RecordHistory(dir, HistoryStatus{c.Attempt, "saved", c.NativeID})
}

func (c *Checkpoint) publish(dir, home string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(dir, ".checkpoint-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(stage) }()
	copied, err := c.copyTranscript(home, filepath.Join(stage, "session.jsonl"))
	if err != nil {
		return err
	}
	if err := writeHistoryJSON(filepath.Join(stage, "checkpoint.json"), copied); err != nil {
		return err
	}
	if err := os.Rename(stage, filepath.Join(dir, c.Attempt)); err != nil {
		return err
	}
	*c = copied
	return syncDirectory(dir)
}

func (c Checkpoint) copyTranscript(home, to string) (Checkpoint, error) {
	root, err := os.OpenRoot(home)
	if err != nil {
		return c, err
	}
	defer func() { _ = root.Close() }()
	src, err := root.Open(c.File)
	if err != nil {
		return c, err
	}
	defer src.Close()
	info, err := src.Stat()
	if err != nil {
		return c, err
	}
	dst, err := os.OpenFile(to, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return c, err
	}
	digest := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(dst, digest), src)
	if err := errors.Join(copyErr, dst.Sync(), dst.Close()); err != nil {
		return c, err
	}
	now, err := src.Stat()
	if err != nil {
		return c, err
	}
	if info.Size() != now.Size() || !info.ModTime().Equal(now.ModTime()) {
		return c, errors.New("native transcript changed during checkpoint")
	}
	if err := validateTranscript(to, c.NativeID, harnessFamily(c.Target)); err != nil {
		return c, err
	}
	c.SHA256 = hex.EncodeToString(digest.Sum(nil))
	return c, nil
}

func findSession(home, target, cwd string, since time.Time) (Checkpoint, error) {
	base := ".claude/projects"
	if harnessFamily(target) == "codex" {
		base = ".codex/sessions"
	}
	origin := filepath.Join(home, filepath.FromSlash(base))
	var newest Checkpoint
	err := filepath.WalkDir(origin, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == origin && errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		if !d.Type().IsRegular() || !strings.HasSuffix(d.Name(), ".jsonl") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.ModTime().Before(since) || !info.ModTime().After(newest.Observed) || info.Size() == 0 {
			return nil
		}
		meta, err := sessionMeta(path, harnessFamily(target))
		if err != nil {
			return err
		}
		if meta.NativeID == "" || !sameSessionDir(meta.CWD, cwd) ||
			(harnessFamily(target) == "claudecode" && (d.Name() != meta.NativeID+".jsonl" || filepath.Base(filepath.Dir(path)) == "subagents")) {
			return nil
		}
		rel, err := filepath.Rel(home, path)
		if err == nil {
			newest = Checkpoint{NativeID: meta.NativeID, File: rel, Observed: info.ModTime().UTC()}
		}
		return err
	})
	return newest, err
}

func sameSessionDir(path, cwd string) bool {
	if path == "" || cwd == "" {
		return false
	}
	return filepath.Clean(path) == filepath.Clean(cwd)
}

type nativeMeta struct{ NativeID, CWD string }

func validateTranscript(path, id, target string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return validateTranscriptFile(f, id, target)
}

func validateTranscriptFile(f *os.File, id, target string) error {
	meta, err := readSessionMeta(f, target)
	if err != nil {
		return err
	}
	if meta.NativeID != id {
		return errors.New("native session ID changed during checkpoint")
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	rows := json.NewDecoder(f)
	for {
		var row json.RawMessage
		err := rows.Decode(&row)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil || len(row) == 0 || row[0] != '{' {
			return errors.New("native transcript holds an incomplete event")
		}
	}
}

func sessionMeta(path, target string) (nativeMeta, error) {
	f, err := os.Open(path)
	if err != nil {
		return nativeMeta{}, err
	}
	defer f.Close()
	return readSessionMeta(f, target)
}

func readSessionMeta(r io.Reader, target string) (nativeMeta, error) {
	in := bufio.NewScanner(r)
	in.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for i := 0; i < 64 && in.Scan(); i++ {
		var row struct {
			Type, SessionID, CWD string
			Payload              struct{ ID, CWD string }
		}
		if json.Unmarshal(in.Bytes(), &row) != nil {
			continue
		}
		if target == "codex" && row.Type == "session_meta" && historyID.MatchString(row.Payload.ID) {
			return nativeMeta{row.Payload.ID, row.Payload.CWD}, nil
		}
		if target == "claudecode" && historyID.MatchString(row.SessionID) && row.CWD != "" {
			return nativeMeta{row.SessionID, row.CWD}, nil
		}
	}
	return nativeMeta{}, in.Err()
}

func FindCheckpoint(dir, nativeID string) (Checkpoint, error) {
	if nativeID == "" {
		var c Checkpoint
		err := readHistoryJSON(filepath.Join(dir, "head.json"), &c)
		return c, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return Checkpoint{}, err
	}
	var newest Checkpoint
	for _, e := range entries {
		if !e.IsDir() || !historyID.MatchString(e.Name()) || e.Name() == "outcomes" {
			continue
		}
		c, err := readCheckpoint(dir, e.Name())
		if err != nil {
			return Checkpoint{}, err
		}
		if c.NativeID == nativeID && (c.Observed.After(newest.Observed) ||
			(c.Observed.Equal(newest.Observed) && c.GitTip != "" && newest.GitTip == "")) {
			newest = c
		}
	}
	if newest.NativeID == "" {
		return Checkpoint{}, os.ErrNotExist
	}
	return newest, nil
}

func RestoreCheckpoint(dir, home string, c Checkpoint) error {
	var trusted Checkpoint
	if !historyID.MatchString(c.Attempt) {
		return errors.New("invalid checkpoint attempt")
	}
	if err := readHistoryJSON(filepath.Join(dir, c.Attempt, "checkpoint.json"), &trusted); err != nil {
		return err
	}
	if c != trusted || !filepath.IsLocal(c.File) || !strings.HasSuffix(c.File, ".jsonl") {
		return errors.New("invalid checkpoint metadata")
	}
	root, err := os.OpenRoot(home)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	if info, err := root.Stat(c.File); err == nil {
		if info.Mode().IsRegular() && !info.ModTime().Before(c.Observed) {
			if current, err := root.Open(c.File); err == nil {
				valid := validateTranscriptFile(current, c.NativeID, harnessFamily(c.Target)) == nil
				_ = current.Close()
				if valid {
					return nil
				}
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	src, err := os.Open(filepath.Join(dir, c.Attempt, "session.jsonl"))
	if err != nil {
		return err
	}
	defer src.Close()
	if err := root.MkdirAll(filepath.Dir(c.File), 0o700); err != nil {
		return err
	}
	tmp := c.File + ".restore-" + rand.Text()
	dst, err := root.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = root.Remove(tmp) }()
	digest := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(dst, digest), src)
	if err := errors.Join(copyErr, dst.Sync(), dst.Close()); err != nil {
		return err
	}
	if hex.EncodeToString(digest.Sum(nil)) != c.SHA256 {
		return errors.New("checkpoint data failed integrity check")
	}
	if err := root.Chtimes(tmp, c.Observed, c.Observed); err != nil {
		return err
	}
	return root.Rename(tmp, c.File)
}

func readCheckpoint(dir, attempt string) (Checkpoint, error) {
	var c Checkpoint
	err := readHistoryJSON(filepath.Join(dir, attempt, "checkpoint.json"), &c)
	return c, err
}

func readHistoryJSON(path string, value any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, value)
}

func writeHistoryJSON(path string, value any) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".history-")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	_, writeErr := f.Write(b)
	if err := errors.Join(writeErr, f.Sync(), f.Close()); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return err
	}
	return syncDirectory(dir)
}

func syncDirectory(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
