package logging

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"voxmesh/internal/version"
)

const (
	LevelError   = 0 // 0 = Solo Errores (Default)
	LevelControl = 1 // 1 = Debug Normal (Control)
	LevelTrace   = 2 // 2 = Debug Avanzado (Trace)
)

var currentLevel atomic.Int32

var state struct {
	sync.Mutex
	logger *log.Logger
	file   *os.File
}

// SetLevel establece el nivel global de logging (0: Error, 1: Control, 2: Trace).
func SetLevel(level int) {
	if level < LevelError {
		level = LevelError
	} else if level > LevelTrace {
		level = LevelTrace
	}
	currentLevel.Store(int32(level))
}

// GetLevel retorna el nivel actual de logging.
func GetLevel() int {
	return int(currentLevel.Load())
}

// IsControl indica si los logs de nivel Control (Nivel 1 o superior) están activos.
func IsControl() bool {
	return currentLevel.Load() >= LevelControl
}

// IsTrace indica si los logs de nivel Trace (Nivel 2) están activos.
func IsTrace() bool {
	return currentLevel.Load() >= LevelTrace
}

func Start() error {
	state.Lock()
	defer state.Unlock()
	if state.file != nil {
		return nil
	}
	root, err := Directory()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return err
	}
	prune(root, time.Now().Add(-48*time.Hour))
	path := filepath.Join(root, "log-"+time.Now().Format("2006-01-02")+".log")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	state.file = file
	state.logger = log.New(file, "", log.LstdFlags|log.Lmicroseconds)
	// Con -H windowsgui no hay stderr: sin esto, un panic fuera de la goroutine principal o un
	// fatal error del runtime (concurrent map writes, deadlock, segfault en cgo) muere sin dejar
	// rastro. El runtime escribe la traza directo a este archivo, con todas las goroutines.
	debug.SetTraceback("all")
	if err := debug.SetCrashOutput(file, debug.CrashOptions{}); err != nil {
		state.logger.Printf("ERROR configurar salida de crash: %v", err)
	}
	state.logger.Printf("=== inicio VoxMesh %s pid=%d %s/%s ===", version.Value, os.Getpid(), runtime.GOOS, runtime.GOARCH)
	return nil
}

func Directory() (string, error) {
	root, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "VoxMesh"), nil
}

// Errorf registra errores (siempre activo, Nivel 0+).
func Errorf(format string, args ...any) {
	state.Lock()
	defer state.Unlock()
	if state.logger != nil {
		state.logger.Printf("ERROR "+format, args...)
	}
}

// Infof registra mensajes de control o informativos (activo en Nivel 1 y Nivel 2).
func Infof(format string, args ...any) {
	if currentLevel.Load() < LevelControl {
		return
	}
	state.Lock()
	defer state.Unlock()
	if state.logger != nil {
		state.logger.Printf("INFO "+format, args...)
	}
}

// Controlf es un alias semántico de Infof para eventos del plano de control (Nivel 1+).
func Controlf(format string, args ...any) {
	Infof(format, args...)
}

// Tracef registra eventos de alta frecuencia, paquetes y bloqueos (activo solo en Nivel 2).
func Tracef(format string, args ...any) {
	if currentLevel.Load() < LevelTrace {
		return
	}
	state.Lock()
	defer state.Unlock()
	if state.logger != nil {
		state.logger.Printf("TRACE "+format, args...)
	}
}

// Panic registra un pánico con su traza de pila completa (Nivel 0+).
func Panic(value any) {
	Errorf("panic: %v\n%s", value, debug.Stack())
}

// Recover captura un recover() y registra el pánico y stack trace si ocurrió.
func Recover(ctx string) {
	if value := recover(); value != nil {
		if ctx != "" {
			Errorf("panic [%s]: %v\n%s", ctx, value, debug.Stack())
		} else {
			Panic(value)
		}
	}
}

func Close() {
	state.Lock()
	defer state.Unlock()
	if state.file != nil {
		// Si un inicio no tiene su cierre, el proceso murió de golpe (crash nativo o kill).
		state.logger.Printf("=== cierre VoxMesh pid=%d ===", os.Getpid())
		_ = debug.SetCrashOutput(nil, debug.CrashOptions{})
		_ = state.file.Close()
	}
	state.file = nil
	state.logger = nil
}

func prune(root string, before time.Time) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "log-") || filepath.Ext(entry.Name()) != ".log" {
			continue
		}
		info, err := entry.Info()
		if err == nil && info.ModTime().Before(before) {
			_ = os.Remove(filepath.Join(root, entry.Name()))
		}
	}
}

func FormatError(err error) string {
	if err == nil {
		return ""
	}
	return fmt.Sprintf("%T: %v", err, err)
}

// TracedRWMutex envuelve sync.RWMutex y provee trazabilidad detallada
// en Nivel 2 (Trace) para detectar bloqueos y deadlocks sin penalizar
// el rendimiento en Nivel 0 o Nivel 1.
type TracedRWMutex struct {
	mu   sync.RWMutex
	name string
}

func NewTracedRWMutex(name string) TracedRWMutex {
	return TracedRWMutex{name: name}
}

func (m *TracedRWMutex) SetName(name string) {
	m.name = name
}

func (m *TracedRWMutex) Name() string {
	if m.name != "" {
		return m.name
	}
	return "RWMutex"
}

func callerFileAndLine() string {
	_, file, line, ok := runtime.Caller(2)
	if !ok {
		return "desconocido"
	}
	return fmt.Sprintf("%s:%d", filepath.Base(file), line)
}

func (m *TracedRWMutex) Lock() {
	if !IsTrace() {
		m.mu.Lock()
		return
	}
	name := m.Name()
	caller := callerFileAndLine()
	Tracef("[%s] esperando Lock() en %s...", name, caller)
	start := time.Now()
	m.mu.Lock()
	Tracef("[%s] adquirido Lock() en %s tras %v", name, caller, time.Since(start))
}

func (m *TracedRWMutex) Unlock() {
	if !IsTrace() {
		m.mu.Unlock()
		return
	}
	name := m.Name()
	caller := callerFileAndLine()
	m.mu.Unlock()
	Tracef("[%s] liberado Lock() en %s", name, caller)
}

func (m *TracedRWMutex) RLock() {
	if !IsTrace() {
		m.mu.RLock()
		return
	}
	name := m.Name()
	caller := callerFileAndLine()
	Tracef("[%s] esperando RLock() en %s...", name, caller)
	start := time.Now()
	m.mu.RLock()
	Tracef("[%s] adquirido RLock() en %s tras %v", name, caller, time.Since(start))
}

func (m *TracedRWMutex) RUnlock() {
	if !IsTrace() {
		m.mu.RUnlock()
		return
	}
	name := m.Name()
	caller := callerFileAndLine()
	m.mu.RUnlock()
	Tracef("[%s] liberado RUnlock() en %s", name, caller)
}

func (m *TracedRWMutex) TryLock() bool {
	if !IsTrace() {
		return m.mu.TryLock()
	}
	name := m.Name()
	caller := callerFileAndLine()
	ok := m.mu.TryLock()
	Tracef("[%s] TryLock() en %s = %v", name, caller, ok)
	return ok
}

func (m *TracedRWMutex) TryRLock() bool {
	if !IsTrace() {
		return m.mu.TryRLock()
	}
	name := m.Name()
	caller := callerFileAndLine()
	ok := m.mu.TryRLock()
	Tracef("[%s] TryRLock() en %s = %v", name, caller, ok)
	return ok
}

func (m *TracedRWMutex) RLocker() sync.Locker {
	return m.mu.RLocker()
}
