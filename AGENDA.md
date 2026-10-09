Actúa como un Senior Go Software Engineer especializado en concurrencia, desarrollo de interfaces con Fyne y procesamiento de audio DSP (CGO). 

**Contexto:** Ya modifiqué a mano el código de VoxMesh (específicamente `internal/ui/ui.go` y el motor de audio en `internal/audio/`). Ya implementé el bypass de RNNoise para gritos, el soft-clip, el uso de `settings.LimiterThreshold` y moví `openHost`/`connectClient` a goroutines. 

**Tu objetivo:** Revisar, unificar y optimizar mi código actual. Solo quiero limpieza, orden, claridad, observabilidad y seguridad (prevenir memory leaks y data races). 

**RESTRICCIÓN ABSOLUTA:** NO cambies la lógica de comportamiento audible que ya programé. NO agregues "mejoras creativas" de sonido, NO introduzcas nuevos features y NO alteres los umbrales de comportamiento salvo que encuentres un bug obvio (nil, race, leak).

Realiza estrictamente lo siguiente:

### 1. REVISIÓN Y OPTIMIZACIÓN DEL MOTOR DE AUDIO (Hot Path)
hecho

### 2. SISTEMA DE LOGGING ESTRUCTURADO (TIERS) EN UI
Reemplaza cualquier booleano de logs en la configuración por un entero `LogLevel` (0 = Solo Errores, 1 = Debug Normal, 2 = Debug Avanzado). En Fyne (tarjeta "Interfaz y Sistema"), agrega un control visual para elegir esto.
- **Nivel 0 (Default):** Solo `logging.Errorf`, `logging.Panic` y capturas de `recover()` con *stack traces*.
- **Nivel 1 (Control):** Conexiones, migraciones, `PacketRoomState`, `PacketRoomHello` y tiempos de ejecución. Cero tráfico continuo.
- **Nivel 2 (Trace):** Todo el tráfico del `receiveLoop` (tipo, tamaño, IP origen de cada paquete UDP, incluyendo audio) y trazas de bloqueos de `sync.RWMutex` para cazar deadlocks.

### 3. CONCURRENCIA NO BLOQUEANTE (`ui.go`)
- Asegura que el `receiveLoop` no bloquee nunca el hilo de Fyne. Verifica que todo uso de `fyne.Do` sea correcto.
- Reduce la contención en `a.peerMu` y `a.roomMu` usando bloqueos de lectura cortos (`RLock`) para clonar datos antes de iterar visualmente.

### ENTREGABLES ESPERADOS:
1. El código refactorizado (o las secciones modificadas) aplicando las mejores prácticas de Go.
2. Una lista corta de los archivos tocados.
3. Un resumen de qué se ordenó/optimizó (locks, allocs, etc.).
4. **Confirmación explícita** al final de que no alteraste la lógica audible (mismos comportamientos).