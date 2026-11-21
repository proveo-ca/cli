// SPEC: _spec/internal/sbx/host-inference.puml (HF IMPORT)
package sbx

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/proveo-ca/proveo/internal/ui"
)

// Import is a Hugging Face model the host's Ollama builds with /api/create when no pull serves it.
type Import struct {
	Repo, Revision string
	Files          []ImportFile
}

// ImportFile is one file of the pinned revision; SHA256 is its LFS digest, "" for a small file.
type ImportFile struct {
	Path   string
	SHA256 string
	Size   uint64
}

var (
	imports     = map[string]Import{}
	hfBase      = "https://huggingface.co"
	importCache = func() string {
		d, err := os.UserCacheDir()
		if err != nil {
			return ""
		}
		return filepath.Join(d, "proveo", "hf")
	}
	freeBytes = diskFree
)

// RegisterImport names the Import that builds model on the host's Ollama.
func RegisterImport(model string, imp Import) { imports[withTag(model)] = imp }

// ImportFor is the Import registered for model.
func ImportFor(model string) (Import, bool) {
	imp, ok := imports[withTag(model)]
	return imp, ok
}

func (imp Import) size() uint64 {
	var n uint64
	for _, f := range imp.Files {
		n += f.Size
	}
	return n
}

// importOllama downloads imp's files, uploads them as blobs and creates model from them; the download is removed after.
func importOllama(model string, imp Import, p *ui.Printer) (uint64, error) {
	if p == nil {
		p = ui.New(io.Discard)
	}
	root := importCache()
	if root == "" {
		return 0, errors.New("no cache directory for the Hugging Face download")
	}
	dir := filepath.Join(root, strings.ReplaceAll(imp.Repo, "/", "--"), imp.Revision)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return 0, err
	}
	need := imp.size()
	if free, ok := freeBytes(dir); ok && free < 2*need-min(need, partialBytes(dir)) {
		return 0, fmt.Errorf("importing %s needs %.1f GiB free (download plus Ollama's copy), %.1f GiB is free under %s",
			imp.Repo, gib(2*need), gib(free), root)
	}
	digests := map[string]string{}
	for i, f := range imp.Files {
		path := filepath.Join(dir, filepath.FromSlash(f.Path))
		sum, err := fetchHF(imp, f, path, fmt.Sprintf("%d/%d", i+1, len(imp.Files)), p)
		if err != nil {
			return 0, fmt.Errorf("download %s/%s: %w", imp.Repo, f.Path, err)
		}
		if f.SHA256 != "" && sum != f.SHA256 {
			_ = os.Remove(path)
			return 0, fmt.Errorf("%s/%s: sha256 %s, pinned %s", imp.Repo, f.Path, sum, f.SHA256)
		}
		if err := pushBlob(path, sum); err != nil {
			return 0, fmt.Errorf("upload %s to the host's Ollama: %w", f.Path, err)
		}
		digests[f.Path] = "sha256:" + sum
	}
	if err := createFromFiles(model, digests, p); err != nil {
		return 0, fmt.Errorf("ollama create %s: %w", model, err)
	}
	_ = os.RemoveAll(dir)
	ui.Logf("imported %s@%s as %s (%.1f GiB)", imp.Repo, imp.Revision, model, gib(need))
	return need, nil
}

func partialBytes(dir string) uint64 {
	var n uint64
	_ = filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if info, err := d.Info(); err == nil {
				n += uint64(info.Size())
			}
		}
		return nil
	})
	return n
}

// fetchHF downloads f at imp's revision to path, resuming a partial file, and returns its sha256.
func fetchHF(imp Import, f ImportFile, path, step string, p *ui.Printer) (string, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	if info, err := os.Stat(path); err == nil && uint64(info.Size()) == f.Size {
		return fileSHA256(path)
	}
	part := path + ".part"
	h := sha256.New()
	have := int64(0)
	if pf, err := os.Open(part); err == nil {
		have, _ = io.Copy(h, pf)
		_ = pf.Close()
	}
	req, err := http.NewRequest(http.MethodGet, hfBase+"/"+imp.Repo+"/resolve/"+imp.Revision+"/"+f.Path, nil)
	if err != nil {
		return "", err
	}
	if have > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", have))
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	switch {
	case resp.StatusCode == http.StatusOK && have > 0:
		h.Reset()
		have = 0
		if err := os.Truncate(part, 0); err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	case resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent:
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	out, err := os.OpenFile(part, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return "", err
	}
	w := &progressWriter{p: p, label: fmt.Sprintf("importing %s %s", imp.Repo, step), done: uint64(have), total: f.Size}
	_, copyErr := io.Copy(io.MultiWriter(out, h, w), resp.Body)
	closeErr := out.Close()
	p.EndLive()
	if err := errors.Join(copyErr, closeErr); err != nil {
		return "", err
	}
	if err := os.Rename(part, path); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

type progressWriter struct {
	p           *ui.Printer
	label       string
	done, total uint64
	last        time.Time
}

func (w *progressWriter) Write(b []byte) (int, error) {
	w.done += uint64(len(b))
	if w.total > 0 && time.Since(w.last) > pullFrameEvery {
		w.last = time.Now()
		w.p.Live(ui.RoleAsync, "%s: %5.1f%% of %.1f GiB", w.label, 100*float64(w.done)/float64(w.total), gib(w.total))
	}
	return len(b), nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, bufio.NewReaderSize(f, 1<<20)); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// pushBlob uploads path to the host's Ollama as blob sha256:sum unless it already holds it.
func pushBlob(path, sum string) error {
	url := hostOllamaURL + "/api/blobs/sha256:" + sum
	if resp, err := http.Head(url); err == nil {
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			return nil
		}
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	resp, err := http.Post(url, "application/octet-stream", f)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("HTTP %d %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return nil
}

// createFromFiles asks the host's Ollama to create model from uploaded blobs named by their repo paths.
func createFromFiles(model string, digests map[string]string, p *ui.Printer) error {
	body, _ := json.Marshal(map[string]any{"model": model, "files": digests, "stream": true})
	resp, err := http.Post(hostOllamaURL+"/api/create", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("HTTP %d %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	defer p.EndLive()
	dec := json.NewDecoder(resp.Body)
	for {
		var ev struct {
			Status string `json:"status"`
			Error  string `json:"error"`
		}
		if err := dec.Decode(&ev); err == io.EOF {
			return nil
		} else if err != nil {
			return err
		}
		switch {
		case ev.Error != "":
			return errors.New(ev.Error)
		case ev.Status != "":
			p.Live(ui.RoleAsync, "creating %s: %s", model, ev.Status)
		}
	}
}
