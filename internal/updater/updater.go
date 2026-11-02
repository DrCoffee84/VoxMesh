package updater

import (
	"crypto/sha256"
	"encoding/hex"
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

	exeAssetName  = "voxmesh.exe"
	hashAssetName = exeAssetName + ".sha256" // lo publica release.yml junto al exe
	maxHashBytes  = 1024

	waitPIDFlag    = "--wait-pid"
	waitPIDTimeout = 15 * time.Second
)

type ReleaseInfo struct {
	Version     string
	DownloadURL string
	SHA256URL   string
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
	exePath, err := executablePath()
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
	return pickRelease(rel, currentVersion)
}

// pickRelease devuelve la actualización si rel es más nueva que currentVersion. Solo acepta el
// asset con el nombre exacto voxmesh.exe y exige su voxmesh.exe.sha256.
func pickRelease(rel githubRelease, currentVersion string) (*ReleaseInfo, error) {
	if !IsNewer(rel.TagName, currentVersion) {
		return nil, nil
	}
	info := &ReleaseInfo{
		Version:   rel.TagName,
		Changelog: strings.TrimSpace(rel.Body),
	}
	for _, asset := range rel.Assets {
		switch asset.Name {
		case exeAssetName:
			info.DownloadURL = asset.BrowserDownloadURL
			info.AssetSize = asset.Size
		case hashAssetName:
			info.SHA256URL = asset.BrowserDownloadURL
		}
	}
	if info.DownloadURL == "" {
		return nil, fmt.Errorf("la versión %s no contiene %s", rel.TagName, exeAssetName)
	}
	if info.SHA256URL == "" {
		return nil, fmt.Errorf("la versión %s no publica %s", rel.TagName, hashAssetName)
	}
	if info.AssetSize < minBinarySize {
		return nil, fmt.Errorf("la versión %s publica un %s inválido (%d bytes)", rel.TagName, exeAssetName, info.AssetSize)
	}
	return info, nil
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

// ApplyUpdate descarga el nuevo ejecutable, verifica tamaño y SHA-256 contra lo publicado y lo
// pone en lugar del actual. No reinicia: la UI cierra la sala y después llama a Relaunch.
func ApplyUpdate(info *ReleaseInfo, onProgress func(fraction float64)) error {
	exePath, err := executablePath()
	if err != nil {
		return err
	}
	return install(&http.Client{Timeout: 5 * time.Minute}, info, exePath, onProgress)
}

// install hace el trabajo de ApplyUpdate sobre exePath. Si algo no coincide con lo publicado,
// borra el .new y deja el ejecutable como estaba.
func install(client *http.Client, info *ReleaseInfo, exePath string, onProgress func(fraction float64)) error {
	tempPath := exePath + ".new"
	oldPath := exePath + ".old"

	_ = os.Remove(tempPath)
	_ = os.Remove(oldPath)

	want, err := fetchSHA256(client, info.SHA256URL)
	if err != nil {
		return err
	}

	resp, err := get(client, info.DownloadURL)
	if err != nil {
		return fmt.Errorf("descargar actualización: %w", err)
	}
	defer resp.Body.Close()

	out, err := os.OpenFile(tempPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return fmt.Errorf("crear archivo temporal de actualización: %w", err)
	}

	hash := sha256.New()
	pw := &progressWriter{
		total:      info.AssetSize,
		onProgress: onProgress,
	}
	// Un byte más que lo publicado alcanza para detectar un archivo más grande sin bajarlo entero.
	written, err := io.Copy(io.MultiWriter(out, hash, pw), io.LimitReader(resp.Body, info.AssetSize+1))
	_ = out.Close()
	if err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("escribir archivo de actualización: %w", err)
	}
	if written != info.AssetSize {
		_ = os.Remove(tempPath)
		return fmt.Errorf("la descarga mide %d bytes y el release publica %d", written, info.AssetSize)
	}
	if got := hex.EncodeToString(hash.Sum(nil)); got != want {
		_ = os.Remove(tempPath)
		return fmt.Errorf("el SHA-256 de la descarga no coincide con el publicado")
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
	return nil
}

// fetchSHA256 baja voxmesh.exe.sha256 ("<hex>  voxmesh.exe", formato sha256sum) y devuelve el
// hash en minúsculas.
func fetchSHA256(client *http.Client, url string) (string, error) {
	if url == "" {
		return "", fmt.Errorf("el release no publica %s", hashAssetName)
	}
	resp, err := get(client, url)
	if err != nil {
		return "", fmt.Errorf("descargar %s: %w", hashAssetName, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxHashBytes))
	if err != nil {
		return "", fmt.Errorf("leer %s: %w", hashAssetName, err)
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return "", fmt.Errorf("%s vacío", hashAssetName)
	}
	sum := strings.ToLower(fields[0])
	if decoded, err := hex.DecodeString(sum); err != nil || len(decoded) != sha256.Size {
		return "", fmt.Errorf("%s no contiene un SHA-256 válido", hashAssetName)
	}
	return sum, nil
}

func get(client *http.Client, url string) (*http.Response, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("el servidor respondió: %s", resp.Status)
	}
	return resp, nil
}

// Relaunch inicia el ejecutable (ya actualizado) con --wait-pid, para que el proceso nuevo no
// abra el log, la config ni los puertos hasta que este termine de cerrar.
func Relaunch() error {
	exePath, err := executablePath()
	if err != nil {
		return err
	}
	cmd := exec.Command(exePath, waitPIDFlag, strconv.Itoa(os.Getpid()))
	cmd.Dir = filepath.Dir(exePath)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("iniciar nueva versión de VoxMesh: %w", err)
	}
	return cmd.Process.Release()
}

// WaitForParent espera, como máximo waitPIDTimeout, a que termine el proceso indicado con
// --wait-pid (el que llamó a Relaunch). Sin el argumento, o si ese proceso ya no existe, vuelve
// enseguida.
func WaitForParent(args []string) {
	pid, ok := waitPIDArg(args)
	if !ok {
		return
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return
	}
	done := make(chan struct{})
	go func() {
		_, _ = proc.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(waitPIDTimeout):
	}
}

func waitPIDArg(args []string) (int, bool) {
	for i := 0; i+1 < len(args); i++ {
		if args[i] != waitPIDFlag {
			continue
		}
		pid, err := strconv.Atoi(args[i+1])
		if err != nil || pid <= 0 || pid == os.Getpid() {
			return 0, false
		}
		return pid, true
	}
	return 0, false
}

func executablePath() (string, error) {
	exePath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("obtener ruta del ejecutable: %w", err)
	}
	exePath, err = filepath.EvalSymlinks(exePath)
	if err != nil {
		return "", fmt.Errorf("resolver enlace del ejecutable: %w", err)
	}
	return exePath, nil
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
