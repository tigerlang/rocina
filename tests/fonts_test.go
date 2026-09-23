package tests

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"rocina/internal/fonts"
)

func TestFontsFallsBackToJetBrains(t *testing.T) {
	dir := t.TempDir()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/monaspace.zip":
			w.WriteHeader(http.StatusNotFound)
		case r.URL.Path == "/jetbrains.ttf":
			_, _ = w.Write([]byte("FAKEFONT"))
		}
	}))
	defer srv.Close()

	fonts.SetSourcesForTest(srv.URL+"/monaspace.zip", srv.URL+"/jetbrains.ttf",
		func() (string, error) { return dir, nil },
		func() []string { return nil })
	result := fonts.Ensure()
	if result.Installed != "JetBrains Mono" {
		t.Fatalf("expected JetBrains fallback, got %+v", result)
	}
	if _, err := os.Stat(filepath.Join(dir, "JetBrainsMonoVar.ttf")); err != nil {
		t.Fatalf("jetbrains font not written: %v", err)
	}
}

func TestFontsFallsBackToSystemDefault(t *testing.T) {
	dir := t.TempDir()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	fonts.SetSourcesForTest(srv.URL+"/m.zip", srv.URL+"/j.ttf",
		func() (string, error) { return dir, nil },
		func() []string { return nil })
	result := fonts.Ensure()
	if result.Installed != "" {
		t.Fatalf("expected no font installed, got %+v", result)
	}
	if result.Message == "" {
		t.Fatal("expected a message about the system default")
	}
}
