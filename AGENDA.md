# 0. Investigación abierta

- [ ] **"Reinicio" del botón en la barra de tareas** (la app sigue: audio y sala no se cortan). Pasa solo, sin tocar nada, en menos de 10 min (22/10 17:34–17:37, pid 16400).
  - El watchdog **nunca** registró un bloqueo de la goroutine 1 en esas sesiones. El hilo de Fyne siguió atendiendo `fyne.Do` y, en el mismo loop, `pollEvents`.
  - 21/10 20:26:59: el Visor de eventos tiene un `Application Hang` (1002) de `voxmesh.exe` pid 7172 ("Top level window is idle"). Esa sesión no tiene `=== cierre` en el log. El 22/10 no hubo evento de hang.
  - Hipótesis descartada para el caso del 22/10 (no se abrió el selector de archivos): hilo de `selectFile` devuelto al pool con ventanas (IME/shell) vivas, ver más abajo.
  - **Reproducido (22/10, monitor externo pasivo):** minimizada y sin tocarla, a los ~3,5 s Windows la marca colgada (`IsHungAppWindow`) dos veces seguidas (18:41:06 y 18:41:11). En ese momento el hilo de la ventana está en `Wait/UserRequest` (no `Suspended`) y **todo el proceso usó 0 ms de CPU en 3,5 s** (26 hilos). El watchdog no registra nada. Ese es el "reinicio": la ventana fantasma de Windows, que al irse vuelve a crear el botón al final de la barra.
  - Interpretación: la goroutine 1 está esperando en el `select` de `runGL` y el tick de 60 Hz (timer de Go) no dispara durante unos 5 s, así que nadie llama `PeekMessage`. El ticker del watchdog tampoco dispara y por eso no ve nada.
  - Con un monitor que le manda `WM_NULL` a la ventana cada 200 ms **no pasó en más de 30 min**: algo que "despierta" al proceso lo evita.
  - ~~Power throttling de Windows~~ **descartado**: con `powercfg /powerthrottling disable` y plan "Alto rendimiento" se colgó igual (18:44:11, 12 s después de minimizar). En ese momento el hilo de UI no usó CPU, pero otros dos hilos sí (~16 ms cada uno): no está congelado todo el proceso, solo el hilo de la ventana.
  - **Por qué el watchdog no lo ve:** el `fyne.Do` del watchdog despierta a la goroutine 1 por el case de `funcQueue`, se ejecuta al toque y el watchdog queda conforme. Pero si el `eventTick` (`time.NewTicker(time.Second/60)` en `runGL`, Fyne `loop.go:125`) no dispara, `pollEvents` (`PeekMessage`) nunca corre. Fyne no frena ese ticker en ningún lado. El watchdog tiene que medir otra cosa: por ejemplo, el tiempo desde el último `pollEvents`/frame, o llamar a `IsHungAppWindow` sobre el propio HWND desde una goroutine.
  - Patrón: una vez que se cuelga, apenas Windows muestra la fantasma, la ventana responde ~200 ms y se vuelve a colgar enseguida (18:41:06 → 18:41:11, 5 s exactos). Parece que el tick no vuelve solo y lo "destraba" algo externo. El monitor que mandaba `WM_NULL` cada 200 ms lo evitaba por completo.
  - ~~Timers de Go~~ **descartado**: con `GODEBUG=asynctimerchan=1` (el exe es go1.26.0, que todavía la respeta) falló igual (18:47:04).
  - **Corrige la interpretación anterior:** un monitor de cambios de contexto (`NtQuerySystemInformation`) muestra que el hilo de la ventana se despierta **~62 veces/s antes, durante y después del cuelgue** (18:48:57). El tick de 60 Hz llega y el hilo no está dormido. Aun así Windows lo marca colgado. O sea: el loop corre pero Windows considera que ese hilo no procesa sus mensajes.
  - Sospechosos ahora: `glfw.PollEvents` / `PeekMessage` mientras está minimizada, el driver de NVIDIA (el proceso tiene ventanas ocultas `NVOpenGLPbuffer` en hilos del driver) o algún mensaje que queda en la cola sin poder sacarse.
  - Próximas pruebas:
    1. Una app Fyne mínima (ventana vacía, Go 1.26 y Fyne 2.6.3) fuera del repo. Si también pasa, es Fyne/GLFW/driver, no VoxMesh. Después, GLFW pelado sin Fyne.
    2. Instrumentar dentro de la app (cuando se pueda tocar código): hora del último `pollEvents` y `GetQueueStatus(QS_ALLINPUT)` desde el hilo de UI cuando el propio HWND da `IsHungAppWindow`.
    3. Probar si pasa sin minimizar (otra ventana encima o un juego en pantalla completa) y estando en una sala con audio.
    4. Los dos `AppHang` de `voxmesh.exe` del 24/09 (`C:\ProgramData\Microsoft\Windows\WER\ReportArchive`) ya cargaban `nvoglv64.dll`: probablemente pasa desde las primeras versiones.
- [ ] **Crashes de mi amigo (cliente)**. Pedir el final del log de la sesión sin `=== cierre` y el "Error de aplicación" del Visor de eventos (módulo con errores). Su log muestra bloqueos de UI con la goroutine 1 `[runnable]` después de llamadas OpenGL: el hilo de UI espera un P, o sea que la CPU está ocupada (GC, otras goroutines o la PC saturada). El watchdog nuevo ya registra CPUs, heap y GC, y las goroutines que ocupan CPU: falta publicar un release.

- [ ] **Cortes de audio ("se corta")**: medir con la versión nueva, todos con nivel de log 1 (2 para grabar llegadas).
  - Quien escucha: `cortes=N (X ms)` por emisor en la línea `Audio de …` (`frases` son pausas, no cortes).
  - Quien habla: `Envío: capturados=… (50.0/s) descartados=… cola máx=…`. Menos de 50/s, descartados o cola alta = su PC/micrófono pierde audio y los demás lo oyen cortado aunque la red ande bien (sospecha: el amigo con Bluetooth; el p95 de 300–400 ms con jitter de 8 ms del log del 22/10 es la firma de eso en la simulación).
  - Con nivel 2 queda `%AppData%\VoxMesh\llegadas-*.csv`: `VOXMESH_ARRIVALS=<csv> go test -run TestJitterReplayArrivals -v ./internal/audio/` reproduce esa red por el jitter buffer.

# 1. Ahora (en este orden)

## 1.1 Seguridad (crítico: arreglar antes de volver a abrir una sala con UPnP)

- [ ] **Path traversal por `Ext`**. `safeName` limpia el ID pero la extensión que llega por red no se valida: `filepath.Join(root, "images", safeName(id)+ext)` ([history.go](internal/history/history.go) `ImagePath`/`SoundPath`).
  - Escritura de archivos arbitrarios (`PacketAssetChunk` → `SaveImage`/`SaveSound`; un `.bat` en Inicio = ejecución de código).
  - Lectura arbitraria (`PacketAssetMissing` → `ReadImage` → se le manda el archivo a quien lo pidió).
  - Borrado (`sound_remove`/sync → `DeleteSound(id, ext)`).
  - Arreglo: lista blanca de extensiones (`.png .jpg .jpeg .gif .webp .wav .mp3 .pcm`) + `os.Root` (`os.OpenRoot` + `root.WriteFile`/`root.Open`) en `history`.
- [ ] **El host acepta paquetes de cualquier dirección** ([network.go](internal/ui/network.go) rama host: `AssetChunk`, `AssetMissing`, `Chat`, `ChatSync`, etc.). Ignorar todo lo que no sea `Hello`/`RoomHello` de direcciones que no estén en la sala. ~~El cliente solo debería aceptar paquetes de la dirección del host~~: hecho (`handleClientPacket` + `Failover.Seen`, ver 1.5).
- [ ] **Bomba de descompresión en miniaturas**: `loadThumbnail` decodifica sin mirar dimensiones (un PNG de 8 MB puede declarar 30000×30000 = ~3,6 GB RGBA). `image.DecodeConfig` antes + límite de píxeles.
- [ ] **`assets.Assembler` sin límites**: tope de transferencias pendientes y de bytes totales en vuelo (por origen).
- [ ] A mediano plazo: clave de sala + AEAD (`chacha20poly1305`) en los datagramas.
- [ ] Updater: verificar el SHA-256 publicado en el release (o firma) antes de reemplazar el `.exe`.

## 1.2 Rendimiento y estabilidad

- [x] **Sacar `debug.FreeOSMemory()` cada 30 s** ([ui_main.go](internal/ui/ui_main.go) en `Run`): reemplazado por `debug.SetMemoryLimit(512 MiB)`. También se sacaron los `runtime.GC()` + `FreeOSMemory()` del soundboard. La RAM vuelve al SO sola, de a poco (en el Administrador de tareas baja más lento que antes).
- [ ] **Chat / miniaturas** ([ui_chat.go](internal/ui/ui_chat.go) `refreshChat`, [ui_chat_thumbnail.go](internal/ui/ui_chat_thumbnail.go)):
  - `refreshChat` reconstruye todas las filas en el hilo de UI, en cada tecla del buscador y una vez por cada imagen recibida (`handleAssetChunk`).
  - Cada reconstrucción lanza una goroutine de decodificación por imagen no cacheada, sin deduplicar ni limitar: con N imágenes, hasta N² decodificaciones en paralelo.
  - Arreglo: `widget.List` (virtualizado), juntar los refresh pendientes (flag sucio + un solo `fyne.Do`), cargas en curso deduplicadas (`singleflight`) + semáforo de 1–2 decodificaciones, `thumbCache` LRU con tope.
- [ ] **Flight recorder en el watchdog**: `runtime/trace.FlightRecorder` (Go 1.25) con los últimos ~10 s; volcarlo a `%AppData%\VoxMesh` cuando el watchdog detecta un bloqueo (`go tool trace`).
- [ ] **Data races**: compilar con `-race` (funciona con CGO) y usarla una sesión real con mi amigo. `a.roomState` sin sincronización es deuda conocida (AGENT.md §H). Arreglo de fondo: un solo dueño (goroutine + canal) y una copia inmutable para lectores, como `a.cfg`.
- [ ] **`selectFile`** ([file_windows.go](internal/ui/file_windows.go)): no devolver al pool el hilo bloqueado después de `GetOpenFileNameW` (queda con ventanas IME/shell que nunca procesan mensajes). Correr el diálogo en una goroutine propia que termine sin `UnlockOSThread`.
- [ ] Revisar los dos avisos de `go vet` (uso dudoso de `unsafe.Pointer`) en [file_windows.go](internal/ui/file_windows.go) (~líneas 162 y 206).
- [ ] `syscall.NewLazyDLL` → `golang.org/x/sys/windows.NewLazySystemDLL` (carga solo desde System32).

## 1.3 Herramientas y estándares

- [ ] CI: `golangci-lint` v2 (`govet`, `staticcheck`, `errcheck`, `gosec`), `govulncheck ./...`, `go test -race -tags nolibopusfile ./...` en el runner de Windows con MSYS2.
- [ ] Tests de cero asignaciones en el camino de audio (`testing.AllocsPerRun` sobre `jitterBuffer.Read`, `sendOutgoingPCM`/`PrepareOutgoingFrames`). Ya está el del callback de captura (`TestEngine_CaptureInputDoesNotAllocate`).
- [ ] `testing/synctest` para lógica con tiempo (jitter buffer, `monitorHost`, `Assembler.Sweep`, watchdog).
- [ ] PGO: perfil de CPU de una sesión real como `cmd/voxmesh/default.pgo`.
- [ ] Perfil de CPU/heap bajo demanda desde Configuración (modo debug), escrito a archivo.
- [ ] `go fix ./...` (modernizers de Go 1.26).
- [ ] Logging con `log/slog` + `slog.LevelVar` en vez de los niveles propios de `internal/logging`.
- [ ] Windows: manifiesto con DPI awareness per-monitor v2 y `longPathAware`; firmar el binario (SmartScreen / falsos positivos de antivirus).

## 1.4 Audio: cortes

Hecho (receptor): esperar el colchón en cada frase nueva, cubrir picos repetidos, PLC de 60 ms en los huecos, stream vivo 60 s, contadores. Simulación (`jitter_sim_test.go`, 10 semillas): Wi-Fi 16 → 3.5 cortes/min, captura que pierde frames 25.9 → 7.1, red estable sin cambios.

- [x] **Emisor: no perder frames de captura** ([engine.go](internal/audio/engine.go) `captureInput`): 25 buffers preasignados (500 ms; antes 8 frames y un `make` por frame en el hilo de tiempo real) y, si igual hay que tirar, la secuencia avanza (`audioTimeline`) y el receptor lo oculta como pérdida. Simulación: 7.1 → 0.5 cortes/min.
- [x] **Emisor: prioridad**: la goroutine de envío corre en un hilo MMCSS "Pro Audio" (`LockAudioThread`).
- [x] **Frase medida en hora de captura**: un atraso al procesar ya no parte la frase (antes el talkspurt se medía con la hora de envío).
- [x] **Sacar los `debug.FreeOSMemory()`** (ver 1.2).
- [x] **Celular** ([server.go](internal/phonemic/server.go) `forwardPCM`): horario fijo que se pone al día en vez del ticker que salteaba ticks, hilo con prioridad de audio, cada frame con su hora en el horario. El procesamiento sigue en esa goroutine: con la recuperación ya no se pierden frames.
- [x] **Archivos con ritmo**: `SendBulk`/`BroadcastBulk`/`TryBroadcastBulk` ([udp.go](internal/transport/udp.go)), token bucket de ~2 Mbps en total para `sendAsset`, `handleAssetMissing` y el reenvío del host. La voz no pasa por esa cola.
- [x] **UPnP**: puertos externos distintos pero siempre hacia el puerto del socket, se anuncia el que funcionó, renovación cada 30 min y al cerrar se borra el mapeado.
- [x] Indicador de voz propia sin un timer por frame (`sweepVoiceIndicator`).

Pendiente, según lo que muestren los logs nuevos:
- [ ] Si la línea `Envío` de alguien muestra **menos de 50 frames/s** (el micrófono o driver entrega menos audio que tiempo, típico del Bluetooth), compensar en el emisor: medir el déficit en una ventana larga y avanzar la secuencia de a un frame cuando se acumulan 20 ms (pérdida que el receptor oculta). No hacerlo a ciegas: un dispositivo con entrega a ráfagas pero ritmo correcto metería pérdidas de más. Mientras tanto, sugerirle el micrófono de la PC o del celular en vez del Bluetooth (además, en Bluetooth el auricular pasa a modo llamada de baja calidad).
- [ ] Solo si después sigue cortándose: time-stretching (acelerar/estirar como NetEq) y P2P directo entre clientes (hoy cliente→host→cliente).

## 1.5 Desconexiones y migración de host

Hecho ([failover.go](internal/room/failover.go), [network_failover.go](internal/ui/network_failover.go), AGENT.md §3.E4):
- El cliente que sale ya no queda vivo por detrás (antes, a los segundos se nombraba host de una sala fantasma).
- Un corte de menos de 20 s no migra: "Reconectando…" y vuelve solo.
- Si el host se va, el host nuevo es el mismo para todos, abren paso entre routers, se reintenta el aviso y nunca quedan dos hosts (epoch); el host que vuelve se suma solo a la sala nueva.
- Sin eco propio, el cliente solo escucha a su host, `Leave` ×3, cambio de rol sin reabrir el audio, `RoomHello` repetido sin avisos dobles (y sin pisar el `Order`, que dejaba órdenes repetidos).

- [ ] **Probar en serio** (nivel de log 1): LAN con 3 instancias en puertos distintos, y por internet con los amigos: (a) el host sale; (b) el host corta el Wi-Fi 10 s; (c) el host corta el Wi-Fi 40 s y vuelve; (d) un cliente sale y vuelve a entrar; (e) un cliente corta el Wi-Fi 15 s. Revisar la actividad y el log: nadie en una sala fantasma ni con dos hosts.
- [ ] Si hay NAT simétrico/CGNAT de los dos lados la migración no pasa (la app avisa). Única salida: un servidor de relay (TURN) o que alguien abra puerto.

## 1.6 Voz fuerte, saturación y Default

- [x] **Modo grito más permisivo por defecto**: entra con −18 dBFS (antes −12), también cuando la voz satura (pico ≥ −1 dBFS, check "Entrar también al saturar") y se mantiene 300 ms. Voz original al gritar: 20 %.
- [x] **Default se actualiza solo**: con el perfil Default activo, al arrancar se toman los valores de fábrica de la versión instalada (`config.ApplyDefaultProfiles`). Si se cambia algo estando en Default, al Guardar se crea solo un perfil "Personalizado" ("Personalizado 2"...).
- [x] **Saturación en rojo**: medidor de Ajustes (también en sala, escucha de prueba y celular) y aro rojo en el avatar. El de los demás llega con `FlagInputClipped` en los paquetes de audio.
- [ ] **Probar con el amigo que habla fuerte**: que actualice y elija el perfil **Default** (hoy está en "Mi configuración", igual que vos). Si su avatar se pone rojo seguido, que baje el volumen del micrófono en Windows: lo que satura antes de llegar a VoxMesh ningún filtro lo arregla.

# 2. Más adelante

- [ ] **Mover `voxmesh.json` a `%AppData%\VoxMesh\`** (junto a `salas\` y `perfiles\`), para que la app sea solo el `.exe`. Migración: si existe el json al lado del exe y no en AppData, moverlo. Revisar [config.Path](internal/config/config.go), el updater (no tiene que tocar el json) y el README. Ojo con la migración de perfiles de `ui.New`: decide "había config" mirando `cfgPath`, tiene que mirar la ruta nueva y la vieja.
