package updater

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func release(tag string, assets ...string) githubRelease {
	rel := githubRelease{TagName: tag}
	for _, name := range assets {
		rel.Assets = append(rel.Assets, struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
			Size               int64  `json:"size"`
		}{Name: name, BrowserDownloadURL: "https://example.invalid/" + name, Size: 2 * minBinarySize})
	}
	return rel
}

func TestPickRelease(t *testing.T) {
	info, err := pickRelease(release("v1.0.0", "voxmesh.exe", "voxmesh.exe.sha256", "voxmesh-windows-x64.zip"), "v0.9.0")
	if err != nil || info == nil {
		t.Fatalf("release válido: info=%v err=%v", info, err)
	}
	if info.DownloadURL != "https://example.invalid/voxmesh.exe" || info.SHA256URL != "https://example.invalid/voxmesh.exe.sha256" || info.AssetSize != 2*minBinarySize {
		t.Fatalf("assets mal elegidos: %+v", info)
	}

	if info, err := pickRelease(release("v0.9.0", "voxmesh.exe", "voxmesh.exe.sha256"), "v0.9.0"); info != nil || err != nil {
		t.Fatalf("misma versión: info=%v err=%v", info, err)
	}

	for name, rel := range map[string]githubRelease{
		"otro .exe":    release("v1.0.0", "otro.exe", "voxmesh.exe.sha256"),
		"mayúsculas":   release("v1.0.0", "VoxMesh.exe", "voxmesh.exe.sha256"),
		"sin .sha256":  release("v1.0.0", "voxmesh.exe"),
		"sin binarios": release("v1.0.0"),
	} {
		if info, err := pickRelease(rel, "v0.9.0"); info != nil || err == nil {
			t.Errorf("%s: se aceptó (info=%v err=%v)", name, info, err)
		}
	}

	small := release("v1.0.0", "voxmesh.exe", "voxmesh.exe.sha256")
	small.Assets[0].Size = minBinarySize - 1
	if info, err := pickRelease(small, "v0.9.0"); info != nil || err == nil {
		t.Errorf("binario chico: se aceptó (info=%v err=%v)", info, err)
	}
}

// updateServer sirve el binario en /voxmesh.exe y, si hash != "", el .sha256.
func updateServer(t *testing.T, binary []byte, hash string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/voxmesh.exe", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(binary) })
	if hash != "" {
		mux.HandleFunc("/voxmesh.exe.sha256", func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(hash + "  voxmesh.exe\n"))
		})
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func sum(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func TestInstall(t *testing.T) {
	current := []byte("version actual")
	binary := bytes.Repeat([]byte("nueva"), 1000)

	tests := []struct {
		name    string
		hash    string
		size    int64
		applied bool
	}{
		{"correcto", sum(binary), int64(len(binary)), true},
		{"hash en mayúsculas", strings.ToUpper(sum(binary)), int64(len(binary)), true},
		{"hash distinto", sum([]byte("otra cosa")), int64(len(binary)), false},
		{"más grande que lo publicado", sum(binary), int64(len(binary)) - 1, false},
		{"más chico que lo publicado", sum(binary), int64(len(binary)) + 1, false},
		{"sin .sha256", "", int64(len(binary)), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			exePath := filepath.Join(dir, "voxmesh.exe")
			if err := os.WriteFile(exePath, current, 0o755); err != nil {
				t.Fatal(err)
			}
			// Un .new viejo no puede sobrevivir a un intento fallido.
			if err := os.WriteFile(exePath+".new", []byte("resto"), 0o755); err != nil {
				t.Fatal(err)
			}
			srv := updateServer(t, binary, tt.hash)
			info := &ReleaseInfo{
				Version:     "v1.0.0",
				DownloadURL: srv.URL + "/voxmesh.exe",
				SHA256URL:   srv.URL + "/voxmesh.exe.sha256",
				AssetSize:   tt.size,
			}

			err := install(srv.Client(), info, exePath, nil)
			if (err == nil) != tt.applied {
				t.Fatalf("err = %v, se esperaba aplicado=%v", err, tt.applied)
			}
			got, readErr := os.ReadFile(exePath)
			if readErr != nil {
				t.Fatal(readErr)
			}
			want := current
			if tt.applied {
				want = binary
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("voxmesh.exe quedó con %d bytes, se esperaban %d", len(got), len(want))
			}
			if _, statErr := os.Stat(exePath + ".new"); !os.IsNotExist(statErr) {
				t.Fatalf("quedó el .new: %v", statErr)
			}
			_, oldErr := os.Stat(exePath + ".old")
			if tt.applied != (oldErr == nil) {
				t.Fatalf(".old: %v (aplicado=%v)", oldErr, tt.applied)
			}
		})
	}
}

func TestWaitPIDArg(t *testing.T) {
	tests := []struct {
		args []string
		pid  int
		ok   bool
	}{
		{nil, 0, false},
		{[]string{"--wait-pid", "1234"}, 1234, true},
		{[]string{"otro", "--wait-pid", "99"}, 99, true},
		{[]string{"--wait-pid"}, 0, false},
		{[]string{"--wait-pid", "abc"}, 0, false},
		{[]string{"--wait-pid", "-5"}, 0, false},
		{[]string{"--wait-pid", "0"}, 0, false},
	}
	for _, tt := range tests {
		pid, ok := waitPIDArg(tt.args)
		if pid != tt.pid || ok != tt.ok {
			t.Errorf("waitPIDArg(%q) = %d, %v; want %d, %v", tt.args, pid, ok, tt.pid, tt.ok)
		}
	}
}

func TestIsNewer(t *testing.T) {
	tests := []struct {
		latest   string
		current  string
		expected bool
	}{
		{"v0.9.1", "0.9.0-build_20260928-2223", true},
		{"0.9.1", "0.9.0", true},
		{"v1.0.0", "v0.9.9", true},
		{"v0.9.0", "0.9.0-build_20260928-2223", false},
		{"v0.9.0", "v0.9.0", false},
		{"v0.8.5", "v0.9.0", false},
		{"v0.9.0", "v1.0.0", false},
		{"v0.10.0", "v0.9.9", true},
		{"v0.9.2", "0.9.1", true},
	}

	for _, tt := range tests {
		got := IsNewer(tt.latest, tt.current)
		if got != tt.expected {
			t.Errorf("IsNewer(%q, %q) = %v; want %v", tt.latest, tt.current, got, tt.expected)
		}
	}
}
