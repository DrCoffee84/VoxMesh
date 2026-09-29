package updater

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	repoOwner       = "DrCoffee84"
	repoName        = "VoxMesh"
	githubLatestURL = "https://api.github.com/repos/" + repoOwner + "/" + repoName + "/releases/latest"
	userAgent       = "VoxMesh-Updater"
	minBinarySize   = 1024 * 1024 // 1 MB mínimo para considerarlo un binario válido
)

type ReleaseInfo struct {
	Version     string
	DownloadURL string
	Changelog   string
	AssetSize   int64
}

type githubRelease struct {
	TagName string `json:"tag_name"`
	Body    string `json:"body"`
	Assets  []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
		Size               int64  `json:"size"`
	} `json:"assets"`
}

// CleanupOldVersions elimina cualquier residuo de actualizaciones previas (.old o .new)
func CleanupOldVersions() {
	exePath, err := os.Executable()
	if err != nil {
		return
	}
	exePath, err = filepath.EvalSymlinks(exePath)
	if err != nil {
		return
	}
	_ = os.Remove(exePath + ".old")
	_ = os.Remove(exePath + ".new")
}

// CheckForUpdate consulta la API de GitHub para verificar si existe una nueva versión
func CheckForUpdate(currentVersion string) (*ReleaseInfo, error) {
	client := &http.Client{Timeout: 6 * time.Second}
	req, err := http.NewRequest("GET", githubLatestURL, nil)
	if err != nil {
		return nil, fmt.Errorf("crear solicitud de actualización: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error de conexión al verificar actualizaciones: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		// No hay releases publicadas aún o el repositorio es privado
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("servidor de actualizaciones respondió: %s", resp.Status)
	}

	var rel githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, fmt.Errorf("decodificar información de release: %w", err)
	}

	if !IsNewer(rel.TagName, currentVersion) {
		return nil, nil
	}

	// Buscar el binario para Windows (.exe)
	var downloadURL string
	var assetSize int64
	for _, asset := range rel.Assets {
		lowerName := strings.ToLower(asset.Name)
		if lowerName == "voxmesh.exe" || strings.HasSuffix(lowerName, ".exe") {
			downloadURL = asset.BrowserDownloadURL
			assetSize = asset.Size
			break
		}
	}

	if downloadURL == "" {
		return nil, fmt.Errorf("la versión %s no contiene un ejecutable voxmesh.exe", rel.TagName)
	}

	return &ReleaseInfo{
		Version:     rel.TagName,
		DownloadURL: downloadURL,
		Changelog:   strings.TrimSpace(rel.Body),
		AssetSize:   assetSize,
	}, nil
}

// progressWriter envuelve io.Writer para reportar el progreso de la descarga
type progressWriter struct {
	total      int64
	downloaded int64
	onProgress func(fraction float64)
}

func (pw *progressWriter) Write(p []byte) (int, error) {
	n := len(p)
	pw.downloaded += int64(n)
	if pw.total > 0 && pw.onProgress != nil {
		pw.onProgress(float64(pw.downloaded) / float64(pw.total))
	}
	return n, nil
}

// ApplyUpdate descarga el nuevo ejecutable, lo reemplaza atómicamente y reinicia la aplicación
func ApplyUpdate(downloadURL string, onProgress func(fraction float64)) error {
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("obtener ruta del ejecutable: %w", err)
	}
	exePath, err = filepath.EvalSymlinks(exePath)
	if err != nil {
		return fmt.Errorf("resolver enlace del ejecutable: %w", err)
	}

	tempPath := exePath + ".new"
	oldPath := exePath + ".old"

	_ = os.Remove(tempPath)
	_ = os.Remove(oldPath)

	client := &http.Client{Timeout: 5 * time.Minute}
	req, err := http.NewRequest("GET", downloadURL, nil)
	if err != nil {
		return fmt.Errorf("crear solicitud de descarga: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("descargar actualización: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("descarga falló con estado: %s", resp.Status)
	}

	out, err := os.OpenFile(tempPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return fmt.Errorf("crear archivo temporal de actualización: %w", err)
	}

	pw := &progressWriter{
		total:      resp.ContentLength,
		onProgress: onProgress,
	}

	_, err = io.Copy(out, io.TeeReader(resp.Body, pw))
	_ = out.Close()
	if err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("escribir archivo de actualización: %w", err)
	}

	fi, err := os.Stat(tempPath)
	if err != nil || fi.Size() < minBinarySize {
		_ = os.Remove(tempPath)
		return fmt.Errorf("archivo descargado inválido o incompleto")
	}

	// Rename and replace: voxmesh.exe -> voxmesh.exe.old, luego voxmesh.exe.new -> voxmesh.exe
	if err := os.Rename(exePath, oldPath); err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("reemplazar ejecutable actual: %w", err)
	}

	if err := os.Rename(tempPath, exePath); err != nil {
		// Rollback si falla
		_ = os.Rename(oldPath, exePath)
		_ = os.Remove(tempPath)
		return fmt.Errorf("instalar nuevo ejecutable: %w", err)
	}

	// Iniciar la nueva versión y salir
	cmd := exec.Command(exePath)
	cmd.Dir = filepath.Dir(exePath)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("iniciar nueva versión de VoxMesh: %w", err)
	}

	os.Exit(0)
	return nil
}

// parseVersionComponents extrae los números major, minor, patch de una cadena de versión
func parseVersionComponents(v string) []int {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if idx := strings.Index(v, "-"); idx != -1 {
		v = v[:idx]
	}
	parts := strings.Split(v, ".")
	nums := make([]int, 0, 3)
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err == nil {
			nums = append(nums, n)
		}
	}
	for len(nums) < 3 {
		nums = append(nums, 0)
	}
	return nums
}

// IsNewer compara dos cadenas de versión y determina si latest es estrictamente mayor que current
func IsNewer(latest, current string) bool {
	l := parseVersionComponents(latest)
	c := parseVersionComponents(current)

	for i := 0; i < 3; i++ {
		if l[i] > c[i] {
			return true
		}
		if l[i] < c[i] {
			return false
		}
	}
	return false
}
