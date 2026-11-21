// SPEC: _spec/internal/sbx/host-inference.puml (HF IMPORT)
package sbx

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

type fakeImportHosts struct {
	mu       sync.Mutex
	files    map[string][]byte // repo path → content
	blobs    map[string]bool
	created  map[string]map[string]string
	ranges   []string
	createOK bool
}

func sum(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

func newFakeImportHosts(t *testing.T, files map[string][]byte) *fakeImportHosts {
	t.Helper()
	f := &fakeImportHosts{files: files, blobs: map[string]bool{}, created: map[string]map[string]string{}, createOK: true}
	hf := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const prefix = "/org/model/resolve/rev1/"
		body, ok := f.files[strings.TrimPrefix(r.URL.Path, prefix)]
		if !strings.HasPrefix(r.URL.Path, prefix) || !ok {
			http.NotFound(w, r)
			return
		}
		if rg := r.Header.Get("Range"); rg != "" {
			f.mu.Lock()
			f.ranges = append(f.ranges, rg)
			f.mu.Unlock()
			from, _ := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(rg, "bytes="), "-"))
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(body[from:])
			return
		}
		_, _ = w.Write(body)
	}))
	ol := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/blobs/sha256:"):
			d := strings.TrimPrefix(r.URL.Path, "/api/blobs/sha256:")
			if r.Method == http.MethodHead {
				if !f.blobs[d] {
					w.WriteHeader(http.StatusNotFound)
				}
				return
			}
			b, _ := io.ReadAll(r.Body)
			if sum(b) != d {
				http.Error(w, "digest mismatch", http.StatusBadRequest)
				return
			}
			f.blobs[d] = true
			w.WriteHeader(http.StatusCreated)
		case r.URL.Path == "/api/create":
			var req struct {
				Model string            `json:"model"`
				Files map[string]string `json:"files"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			if !f.createOK {
				_, _ = w.Write([]byte(`{"status":"parsing"}` + "\n" + `{"error":"unsupported architecture"}` + "\n"))
				return
			}
			f.created[req.Model] = req.Files
			_, _ = w.Write([]byte(`{"status":"writing manifest"}` + "\n" + `{"status":"success"}` + "\n"))
		case r.URL.Path == "/api/tags":
			var models []string
			for m := range f.created {
				models = append(models, fmt.Sprintf(`{"name":%q,"size":%d}`, m, 1<<30))
			}
			_, _ = w.Write([]byte(`{"models":[` + strings.Join(models, ",") + `]}`))
		case r.URL.Path == "/api/ps":
			_, _ = w.Write([]byte(`{"models":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(hf.Close)
	t.Cleanup(ol.Close)
	origHF, origOL, origCache, origFree := hfBase, hostOllamaURL, importCache, freeBytes
	t.Cleanup(func() { hfBase, hostOllamaURL, importCache, freeBytes = origHF, origOL, origCache, origFree })
	cache := t.TempDir()
	hfBase, hostOllamaURL = hf.URL, ol.URL
	importCache = func() string { return cache }
	freeBytes = func(string) (uint64, bool) { return 1 << 40, true }
	return f
}

func testImport(files map[string][]byte, pinned ...string) Import {
	imp := Import{Repo: "org/model", Revision: "rev1"}
	for _, p := range []string{"model-00001-of-00002.gguf", "model-00002-of-00002.gguf", "config/tokenizer.json"} {
		if b, ok := files[p]; ok {
			f := ImportFile{Path: p, Size: uint64(len(b))}
			for _, pin := range pinned {
				if pin == p {
					f.SHA256 = sum(b)
				}
			}
			imp.Files = append(imp.Files, f)
		}
	}
	return imp
}

func TestImportDownloadsVerifiesUploadsAndCreates(t *testing.T) {
	files := map[string][]byte{
		"model-00001-of-00002.gguf": []byte(strings.Repeat("a", 4096)),
		"model-00002-of-00002.gguf": []byte(strings.Repeat("b", 2048)),
		"config/tokenizer.json":     []byte(`{"v":1}`),
	}
	f := newFakeImportHosts(t, files)
	imp := testImport(files, "model-00001-of-00002.gguf", "model-00002-of-00002.gguf")
	got, err := importOllama("proveo/model:q4", imp, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != imp.size() {
		t.Errorf("imported %d bytes, want %d", got, imp.size())
	}
	want := map[string]string{}
	for p, b := range files {
		want[p] = "sha256:" + sum(b)
	}
	if fmt.Sprint(f.created["proveo/model:q4"]) != fmt.Sprint(want) {
		t.Errorf("create files = %v, want each repo path mapped to its blob %v", f.created["proveo/model:q4"], want)
	}
	if entries, _ := os.ReadDir(filepath.Join(importCache(), "org--model")); len(entries) != 0 {
		t.Errorf("the download must be removed after create, left %v", entries)
	}
}

func TestImportResumesAPartialDownload(t *testing.T) {
	body := []byte(strings.Repeat("xyz", 1000))
	files := map[string][]byte{"model-00001-of-00002.gguf": body}
	f := newFakeImportHosts(t, files)
	imp := testImport(files, "model-00001-of-00002.gguf")
	part := filepath.Join(importCache(), "org--model", "rev1", "model-00001-of-00002.gguf.part")
	if err := os.MkdirAll(filepath.Dir(part), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(part, body[:1200], 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := importOllama("proveo/model:q4", imp, nil); err != nil {
		t.Fatal(err)
	}
	if len(f.ranges) != 1 || f.ranges[0] != "bytes=1200-" {
		t.Errorf("range requests = %v, want one resume from byte 1200", f.ranges)
	}
}

func TestImportRefusesAFileThatDoesNotMatchItsPin(t *testing.T) {
	files := map[string][]byte{"model-00001-of-00002.gguf": []byte("tampered")}
	f := newFakeImportHosts(t, files)
	imp := testImport(files)
	imp.Files[0].SHA256 = sum([]byte("original"))
	_, err := importOllama("proveo/model:q4", imp, nil)
	if err == nil || !strings.Contains(err.Error(), "pinned") {
		t.Fatalf("want a pin mismatch, got %v", err)
	}
	if len(f.blobs) != 0 || len(f.created) != 0 {
		t.Errorf("a mismatched file must reach no blob and no model: blobs %v created %v", f.blobs, f.created)
	}
}

func TestImportNeedsRoomForTheDownloadAndOllamasCopy(t *testing.T) {
	files := map[string][]byte{"model-00001-of-00002.gguf": []byte(strings.Repeat("a", 4096))}
	newFakeImportHosts(t, files)
	freeBytes = func(string) (uint64, bool) { return 4096, true }
	_, err := importOllama("proveo/model:q4", testImport(files), nil)
	if err == nil || !strings.Contains(err.Error(), "needs") {
		t.Fatalf("want a disk refusal, got %v", err)
	}
}

func TestImportSurfacesOllamasCreateError(t *testing.T) {
	files := map[string][]byte{"model-00001-of-00002.gguf": []byte("weights")}
	f := newFakeImportHosts(t, files)
	f.createOK = false
	if _, err := importOllama("proveo/model:q4", testImport(files), nil); err == nil || !strings.Contains(err.Error(), "unsupported architecture") {
		t.Fatalf("want Ollama's create error, got %v", err)
	}
}

func TestEnsureHostOllamaImportsARegisteredModel(t *testing.T) {
	files := map[string][]byte{"model-00001-of-00002.gguf": []byte("weights")}
	f := newFakeImportHosts(t, files)
	availableMemory = func() (hostMemory, bool) { return hostMemory{Free: 1 << 40, Pool: poolUnified}, true }
	t.Cleanup(func() { availableMemory = hostAvailableMemory })
	RegisterImport("proveo/model:q4", testImport(files))
	t.Cleanup(func() { delete(imports, "proveo/model:q4") })
	if _, err := EnsureHostOllama("proveo/model:q4", 0, noEnv, nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.created["proveo/model:q4"]; !ok {
		t.Errorf("a registered model must be imported, created = %v", f.created)
	}
}
