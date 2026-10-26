# AGENDA VoxMesh

## Cómo usar esta agenda (leer antes de tomar una tarea)

Cada tarea pendiente tiene un **ID** (`S1`, `R2`, `F3`...) y está escrita para que un chat nuevo la pueda resolver sin el contexto de otros chats. Regla: **un chat = una tarea (o un grupo que la tarea diga explícitamente)**.

1. Leer [AGENT.md](AGENT.md) completo (mapa técnico, reglas de hilos, build con CGO) y la tarea entera, incluidas sus "Dependencias".
2. Antes de tocar código, verificar que lo que describe la tarea sigue siendo cierto (los números de línea son de 2026-10-26 y se corren; buscar por nombre de función).
3. No tocar archivos ni comportamientos fuera del alcance de la tarea. Si aparece otro problema, anotarlo como tarea nueva acá (con ID nuevo) en vez de arreglarlo de paso.
4. Build y tests **siempre con CGO**: `CGO_ENABLED=1`, `CC=C:\msys64\ucrt64\bin\gcc.exe`, `-tags nolibopusfile` (AGENT.md §4). Sin eso `internal/ui` y el Engine no compilan y los tests "pasan" vacíos.
5. Al terminar: marcar `[x]`, dejar debajo una línea **"Hecho:"** con qué se cambió (archivos/funciones) y qué quedó afuera, y **actualizar AGENT.md** si cambió una regla, un protocolo o la estructura. Si cambió algo visible para el usuario, también [README.md](README.md).
6. Commits chicos, uno por tarea, mensaje en castellano. No commitear si los tests fallan.

### Orden sugerido para lo pendiente

| # | ID | Qué | Por qué en este orden |
|---|----|-----|-----------------------|
| 1 | **S1** | Path traversal por extensión | Escritura/lectura/borrado de archivos arbitrarios desde internet. Chico y aislado. |
| 2 | **S2** | El host solo atiende a participantes | Hoy un desconocido con un paquete escucha toda la sala. |
| 3 | **S3** | Identidad atada a la dirección | Sin esto, un participante (o desconocido) se hace pasar por otro, echa gente o roba la sala. Va después de S2 (comparten la tabla dirección → participante). |
| 4 | **S5**, **S6** | Bomba de descompresión, límites del Assembler | DoS por memoria. Independientes entre sí. |
| 5 | **S7**, **R3** | Updater: verificar binario y cerrar prolijo | Chicos. Se pueden hacer en el mismo chat. |
| 6 | **R1** | `a.roomState` con un solo dueño | Data race real en la ruta caliente del host. Más grande: hacerlo con S2/S3 ya terminados. |
| 7 | **T1** | CI con tests y `-race` | Para que lo anterior no se rompa. |
| 8 | **F2** | Push-to-talk | Barato, el diseño ya está. |
| 9 | **S4** + **F3** | Clave de sala + AEAD, link de invitación | Cambio de protocolo (rompe compatibilidad): juntos en un release. |
| 10 | **F1** | Cancelación de eco | La más grande; conviene con lo demás estable. |

---

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
- [x] **Amigo sin audio de salida (25/10)** después de desconectar el Bluetooth y enchufar HDMI + auriculares. El log no mostró nada: tenía nivel 0 y `Audio no disponible` solo iba a la barra de estado. Causa probable: la salida estaba guardada por nombre, ese nombre ya no existía y `NewEngine` fallaba entero (`dispositivo no encontrado`). Hecho:
  - Dispositivos por ID de Windows. Si no está, se usa el predeterminado y se olvida el guardado. Pasa al entrar a la sala y con **Refresh Audio** (también fuera de la sala, y suelta el reproductor del soundboard).
  - Log nivel 1 por defecto (el 0 ya no existe).
  - El error de audio va al log como ERROR.
  - Ver AGENT.md §3.I "Devices".
  - Falta: probarlo con el amigo. Más adelante quizás: avisar solo cuando un dispositivo se desconecta en medio de la sala (callback `Stop` de malgo), sin esperar a que toque Refresh.
- [ ] **Crashes de mi amigo (cliente)**. Pedir el final del log de la sesión sin `=== cierre` y el "Error de aplicación" del Visor de eventos (módulo con errores). Su log muestra bloqueos de UI con la goroutine 1 `[runnable]` después de llamadas OpenGL: el hilo de UI espera un P, o sea que la CPU está ocupada (GC, otras goroutines o la PC saturada). El watchdog nuevo ya registra CPUs, heap y GC, y las goroutines que ocupan CPU: falta publicar un release.

- [ ] **Cortes de audio ("se corta")**: medir con la versión nueva, todos con nivel de log 1 (2 para grabar llegadas).
  - Quien escucha: `cortes=N (X ms)` por emisor en la línea `Audio de …` (`frases` son pausas, no cortes).
  - Quien habla: `Envío: capturados=… (50.0/s) descartados=… cola máx=…`. Menos de 50/s, descartados o cola alta = su PC/micrófono pierde audio y los demás lo oyen cortado aunque la red ande bien (sospecha: el amigo con Bluetooth; el p95 de 300–400 ms con jitter de 8 ms del log del 22/10 es la firma de eso en la simulación).
  - Con nivel 2 queda `%AppData%\VoxMesh\llegadas-*.csv`: `VOXMESH_ARRIVALS=<csv> go test -run TestJitterReplayArrivals -v ./internal/audio/` reproduce esa red por el jitter buffer.

# 1. Ahora (en este orden)

## 1.1 Seguridad (crítico: arreglar antes de volver a abrir una sala con UPnP)

**Situación (revisión del 2026-10-26).** El host expone un puerto UDP a internet (UPnP) y hoy no autentica nada: los IDs, nombres y extensiones que vienen dentro de los paquetes se usan tal cual. El lado cliente ya está bien: solo acepta audio, chat, archivos y estado de **su host** (`handleClientPacket` + `Failover.Seen`, ver 1.5). S1–S3 cierran lo grave sin cambiar el protocolo; S4 (clave de sala) es la solución de fondo y rompe compatibilidad.

### S1. Validar la extensión (y el ID) de los archivos que llegan por red

- [ ] **Problema.** [history.go](internal/history/history.go) arma rutas con `filepath.Join(s.root, "images"|"sounds", safeName(id)+extension)` (`ImagePath` ~152, `SoundPath` ~163). `safeName` (~469) limpia `/` y `\` del ID, pero **la extensión no se valida** y viene de la red (`assets.Chunk.Ext`, `assets.MissingRequest.Ext`, `history.Message.ImageExt`, `room.Sound.Ext`). Como `filepath.Join` resuelve `..`, las operaciones de `SaveImage`, `ReadImage`, `SaveSound`, `ReadSound` y `DeleteSound` pueden terminar **fuera de la carpeta de la sala** (escritura, lectura y borrado). Entradas afectadas: `handleAssetChunk` y `handleAssetMissing` ([network_sync.go](internal/ui/network_sync.go)), `dropSounds` ([sound_sync.go](internal/ui/sound_sync.go)), la fila de imagen del chat ([ui_chat.go](internal/ui/ui_chat.go) ~302).
  - `safeName` tampoco filtra `:` ni los nombres reservados de Windows (`CON`, `NUL`, `COM1`…), y también se aplica al **nombre de la sala**, que al cliente se lo manda el host.
- **Arreglo.**
  - Una única función de validación en `internal/history`, usada por todos los helpers de ruta y por `Save*`/`Read*`/`HasImage`/`DeleteSound`/`UpdateSoundMeta` (devuelven error si no pasa):
    - Extensión en **lista blanca** (en minúsculas): imágenes `.png .jpg .jpeg .gif .webp` (lo que acepta el selector, [file_windows.go](internal/ui/file_windows.go)); sonidos `.pcm` (lo único que produce la app desde el recorte) y `.wav` (default de `SaveSound` y manifests viejos).
    - ID: `history.NewID` genera 32 caracteres hex. Exigir `[0-9a-f]{32}`; antes revisar que los manifests existentes (`salas\*\sounds\manifest`) cumplan, y si no, aceptar `[A-Za-z0-9_-]{1,64}`.
    - Nombre de sala: sacar también `<>:"|?*` y caracteres de control; a los reservados agregarles `_`.
  - Defensa en profundidad: `os.OpenRoot` sobre la carpeta de la sala y `root.WriteFile`/`root.ReadFile`/`root.Remove` (Go 1.25+), así el SO tampoco deja salir de ahí.
  - Descartar temprano en las entradas de red y loguear a nivel 1 como máximo una línea cada 10 s.
  - `sendChatImageBytes` ([ui_chat.go](internal/ui/ui_chat.go) ~108): rechazar extensiones fuera de la lista (el selector permite "Todos los archivos").
- **Tests** (`history_test.go`, sin CGO): extensiones con separadores, `..`, `:`, absolutas o vacías → error y ningún archivo fuera de `t.TempDir()`; `.PNG` se acepta normalizada; ID inválido → error.
- **Hecho cuando**: ningún string de red llega a `filepath.Join`/`os.*` sin pasar por la validación.

### S2. El host atiende solo a participantes

- [ ] **Problema.** En [network.go](internal/ui/network.go), `handlePacket` (~70) hace `udp.AddPeer(addr)` y reenvía el audio antes de mirar quién lo manda, y `handleHostPacket` (~87) hace `AddPeer` con cualquier paquete válido. La lista de peers es la de difusión, así que **cualquier dirección que mande un paquete pasa a recibir lo que reenvía el host** (audio, chat, estado de la sala con las IP de todos, archivos), sin aparecer en la lista de participantes. Esos peers nunca se sacan (`RemovePeer` solo en `handleParticipantLeave` y al pasar a cliente) y `pingPeers` (~548) les manda ping cada 2 s.
  - Los demás handlers del host tampoco miran el origen: chat (`handleChatPacket`), historial (`handleSyncPacket`, que además reenvía el pedido a los clientes con `ReplyTo`), archivos (`handleAssetMissing`), botonera (`handleSoundSyncRequest` aplica tombstones de cualquiera) y salida (`handleParticipantLeave` busca por ID sin mirar la dirección).
  - `room.State.Participants` no tiene tope ni poda: cada `RoomHello` con un ID nuevo agrega uno. Pasa también sin mala intención: `ParticipantID = sha256(usuario|dirección)`, así que reconectar desde otro puerto crea un ID nuevo. El `RoomState` viaja en un solo datagrama.
- **Arreglo.**
  - `ParticipantByAddress(addr string) (Participant, bool)` en [state.go](internal/room/state.go).
  - Un filtro único al principio del lado host (antes del reenvío de `handlePacket` y del `switch` de `handleHostPacket`): de una dirección que no es participante solo se atiende `PacketRoomHello`; `PacketPing`/`PacketPong`/`PacketRoomState` también si la dirección figura en la lista aunque esté desconectada (durante una migración llegan antes del `RoomHello`: revisar `takeOverHost` y [failover.go](internal/room/failover.go)). El resto se descarta sin efectos.
  - `AddPeer` solo al aceptar un `RoomHello` y donde ya lo hace la migración ([network_failover.go](internal/ui/network_failover.go) ~110). `RemovePeer` cuando un participante se va, vence o cambia de dirección.
  - El host no acepta de clientes `room.SystemMessage` ni mensajes marcados como sistema: esos avisos solo los genera el host (`addSystemMessage`, [ui_chat.go](internal/ui/ui_chat.go) ~455).
  - `sync_request`: que conteste solo el host, o que los clientes respondan solo a direcciones de participantes.
  - Tope de participantes (por ejemplo 16), poda de desconectados viejos (por ejemplo > 10 min; `Candidates()` ya ignora desconectados) y límite de `RoomHello` de direcciones nuevas.
  - Contador de paquetes descartados de desconocidos, logueado a nivel 1 como máximo cada 10 s.
  - Lo que queda abierto sin S4: cualquiera con la dirección puede entrar con un `RoomHello`, pero ahora aparece en la lista.
- **Tests**: la decisión "¿atiendo este tipo de paquete de esta dirección?" como función pura con tabla de casos; `ParticipantByAddress`; tope y poda. A mano: migración de 1.5 (casos a–e).
- **Hecho cuando**: un paquete que no es `RoomHello` y viene de una dirección desconocida no tiene ningún efecto, y la migración sigue andando.

### S3. Identidad atada a la dirección

- [ ] **Dependencias**: S2 (misma tabla dirección → participante).
- **Problema.** Se cree el ID que viene en el payload:
  - `handleRoomHello` ([network.go](internal/ui/network.go) ~204): un ID existente que llega desde otra dirección reemplaza el registro anterior.
  - Audio (`handleAudioPacket`, [network_audio.go](internal/ui/network_audio.go)): ID del emisor tomado del payload.
  - Chat: `SenderID`/`Username`/`System` del payload.
  - `RoomLeave`: por ID.
  - `Failover.StateFrom` ([failover.go](internal/room/failover.go) ~239): un `RoomState` con `HostID` conocido y epoch mayor hace que el host se baje (`ActStepDown`) y los clientes se cambien (`ActSwitchHost`) hacia la dirección **que mandó el paquete**, sin comprobar que sea la del `HostID`.
  - Ya hay un buen modelo: `handleMutePacket` → `State.SetMuted(id, addr, …)` solo acepta desde la dirección registrada.
- **Arreglo.**
  - Host: la identidad sale de la dirección (`ParticipantByAddress`). Audio: el ID del payload tiene que coincidir; si no, se descarta antes de reenviar (el lookup es barato; el reenvío sigue siendo lo primero que se hace después). Chat: el host pisa `SenderID`/`Username` con los del participante y saca `System` antes de guardar y reenviar. `RoomLeave`: solo desde la dirección del participante.
  - `RoomHello` con un ID que ya existe en otra dirección: rechazarlo mientras la dirección vieja siga viva (visto hace < 6 s); si está callada, aceptarlo (reconexión legítima o cambio de NAT) y loguearlo.
  - `StateFrom`: aceptar solo si `from` coincide con la dirección guardada del `HostID`. **Cuidado**: los participantes de la misma LAN que el host viejo están guardados con dirección privada y la respuesta puede venir de otra (AGENT.md §3.E4 "Limits"). Hacer el chequeo estricto, correr `failover_test.go` y probar el caso LAN de 1.5; si hace falta una excepción, documentarla. Con S4 esto queda limitado a miembros.
- **Tests**: en `failover_test.go`, un `StateFrom` con `HostID` conocido pero `from` distinto → ninguna acción. Funciones puras del lado host para audio, chat y salida.

### S4. Clave de sala + cifrado autenticado de los datagramas

- [ ] **Dependencias**: S2 y S3. Conviene en el mismo release que F3 (el link lleva la clave). **Rompe compatibilidad** (como el cambio a Opus: todos actualizan).
- **Diseño propuesto.**
  - Clave aleatoria de 32 bytes que crea "Iniciar sala" y se guarda por sala (en `salas\<sala>\`), así reiniciar el host no invalida la invitación. Los clientes la guardan al entrar: si migra el host, todos la tienen.
  - AEAD por paquete: **AES-256-GCM de la stdlib** (sin dependencias nuevas, AES-NI en cualquier x64) o `chacha20poly1305` de `x/crypto`. Los 16 bytes de cabecera van en claro como datos asociados (AAD); el payload lleva nonce + texto cifrado + tag (+28 bytes: el audio pasa de ~130 a ~158 bytes, unos +11 kbps por emisor).
  - Nonce: 4 bytes aleatorios por sesión de socket + contador de 64 bits (nunca se repite con la misma clave). Ventana anti-repetición por emisor (64 paquetes) para los paquetes de control. El audio ya descarta duplicados y tardíos en el jitter buffer.
  - El host verifica antes de reenviar y reenvía los mismos bytes (todos comparten la clave: no hace falta volver a cifrar).
  - Magic nuevo (`VM02`). Un paquete que no se puede abrir se descarta en silencio. El cliente con la clave equivocada ve "clave incorrecta o host inalcanzable" después del timeout.
  - Límite que se acepta: con una clave compartida, un miembro puede falsificar paquetes de otro miembro. S3 lo acota con las direcciones.
  - El micrófono del celular no cambia (es LAN y tiene su propio token).
- **Tests**: ida y vuelta; paquete alterado → rechazo; repetido → rechazo; clave equivocada → rechazo; `Seal`/`Open` sin asignaciones en el camino del audio (buffers reutilizados).
- **Docs**: AGENT.md §3.C (formato del paquete) y README (cómo invitar).

### S5. Imágenes: límite de dimensiones y nada de decodificar en el hilo de UI

- [ ] **Problema.** `loadThumbnail` ([ui_chat_thumbnail.go](internal/ui/ui_chat_thumbnail.go) ~52) hace `image.Decode` sin mirar dimensiones: un archivo chico puede declarar dimensiones enormes y reservar gigas. El lightbox ([ui_components.go](internal/ui/ui_components.go) ~372) usa `canvas.Image{File: …}`, que decodifica la imagen completa **en el hilo de UI** (congela la ventana; AGENT.md §3.G lo prohíbe para el chat).
- **Arreglo.** `image.DecodeConfig` antes de decodificar y rechazar si pasa un tope (por ejemplo 40 MP o un lado > 16384). Validarlo también al recibir (`handleAssetChunk`) y no guardar lo que no sea una imagen válida. Lightbox: decodificar fuera del hilo de UI a un tamaño de pantalla (reusar `loadThumbnail` con otro máximo) y mostrar el resultado con `fyne.Do`.
- **Tests**: una cabecera PNG que declara dimensiones gigantes → error sin reservar memoria (solo se lee la cabecera).

### S6. `assets.Assembler` con límites

- [ ] **Problema.** `Assembler.pending` ([assets.go](internal/assets/assets.go) ~154/165) no tiene tope: cada ID nuevo abre una transferencia de hasta `MaxBytes` (8 MiB) que solo se libera cuando `Sweep` se rinde (32 intentos). Muchas transferencias incompletas a la vez pueden agotar la memoria (el límite blando es 512 MiB), y cada `Sweep` manda `AssetMissing` al origen.
- **Arreglo.** Topes de transferencias pendientes en total (por ejemplo 16) y por origen (por ejemplo 4), y de bytes en vuelo (por ejemplo 32 MiB). Pasado el tope, descartar lo nuevo (no lo viejo). Con S2, solo los participantes llegan acá, pero el tope sigue haciendo falta.
- **Tests** (`assets`, sin CGO): pasar los topes descarta; completar o vencer libera el espacio.

### S7. Updater: verificar lo que se descarga

- [ ] **Problema** ([updater.go](internal/updater/updater.go)). `CheckForUpdate` (~93) elige `voxmesh.exe` **o cualquier `.exe`** del release. `ApplyUpdate` no verifica nada además de un tamaño mínimo: ni hash, ni firma, ni que el tamaño coincida con `AssetSize` (que se lee y no se usa). HTTPS ya protege el camino; lo que falta es verificar que el archivo sea el que se publicó.
- **Arreglo.**
  - Exigir el nombre exacto `voxmesh.exe`.
  - `release.yml` publica `voxmesh.exe.sha256`; el updater lo baja, compara y además chequea el tamaño contra `AssetSize`. Esto protege contra descargas corruptas o truncadas.
  - Opcional, más fuerte: una firma ed25519 (`voxmesh.exe.sig`) con la clave pública embebida en el binario. Si la clave privada está en los secrets de Actions, protege contra el reemplazo de assets del release, pero no contra un repo o CI comprometidos.
  - Junto con R3 (cerrar prolijo antes de relanzar).
- **Tests**: hash que no coincide → no reemplaza el exe y borra el `.new`.

### S8. Privacidad: no mandar la ruta local de las imágenes

- [ ] **Problema.** `history.Message.ImagePath` (`json:"image_path"`) lleva la ruta absoluta local (`C:\Users\<usuario de Windows>\AppData\…`) y viaja a la sala con cada imagen ([ui_chat.go](internal/ui/ui_chat.go) ~130 y ~582). Queda en el historial de todos. Nadie la lee: el receptor recalcula la ruta con `ImagePath(id, ext)`.
- **Arreglo.** `json:"-"` o sacar el campo (verificar con `grep -rn "\.ImagePath\b"` que nada lo lea). Los historiales viejos que lo tengan se pueden dejar así.
- (Conocido, sin acción: el `RoomState` lleva la IP:puerto pública de cada participante a todos. La migración de host la necesita.)

### S9. Micrófono del celular: detalles menores

- [ ] `handlePage`/`handleWebSocket` ([server.go](internal/phonemic/server.go) ~257/266) comparan el token con `!=`: usar `subtle.ConstantTimeCompare` como ya hace la ruta UDP. `websocket.Accept` usa `InsecureSkipVerify: true` (no chequea el `Origin`): limitarlo al host propio si no rompe la página. Prioridad baja (LAN y con token).

## 1.2 Rendimiento y estabilidad

- [x] **Sacar `debug.FreeOSMemory()` cada 30 s** ([ui_main.go](internal/ui/ui_main.go) en `Run`): reemplazado por `debug.SetMemoryLimit(512 MiB)`. También se sacaron los `runtime.GC()` + `FreeOSMemory()` del soundboard. La RAM vuelve al SO sola, de a poco (en el Administrador de tareas baja más lento que antes).
- [ ] **R2. Chat / miniaturas** ([ui_chat.go](internal/ui/ui_chat.go) `refreshChat`, [ui_chat_thumbnail.go](internal/ui/ui_chat_thumbnail.go)):
  - `refreshChat` reconstruye todas las filas en el hilo de UI, en cada tecla del buscador y una vez por cada imagen recibida (`handleAssetChunk`).
  - Cada reconstrucción lanza una goroutine de decodificación por imagen no cacheada, sin deduplicar ni limitar: con N imágenes, hasta N² decodificaciones en paralelo.
  - Arreglo: `widget.List` (virtualizado), juntar los refresh pendientes (flag sucio + un solo `fyne.Do`), cargas en curso deduplicadas (`singleflight`) + semáforo de 1–2 decodificaciones, `thumbCache` LRU con tope. El lightbox va con S5.
- [ ] **Flight recorder en el watchdog**: `runtime/trace.FlightRecorder` (Go 1.25) con los últimos ~10 s; volcarlo a `%AppData%\VoxMesh` cuando el watchdog detecta un bloqueo (`go tool trace`).

### R1. `a.roomState` con un solo dueño (data race en la ruta caliente del host)

- [ ] **Dependencias**: mejor después de S2/S3 (tocan las mismas funciones).
- **Problema** (AGENT.md §H). `a.roomState` (`room.State`, con slice de participantes) se lee y escribe sin sincronización desde varias goroutines:
  - `receiveLoop` → `handleHostPacket` ([network.go](internal/ui/network.go) ~86): actualiza `LastSeenUTC` en **cada paquete**; `handleRoomHello`, `handleParticipantLeave`, `handleMutePacket`.
  - `pingPeers` (~548): vencimientos.
  - `failoverLoop` ([network_failover.go](internal/ui/network_failover.go)): `takeOverHost`, `switchHost`, `stepDown`.
  - `configureHostNetwork`, `saveSettings`, y lectores como `sendAsset` y `addSystemMessage` (`a.roomState.HostID == a.participantID`).
  - `a.participantID` y `a.activeRoomName` son campos comunes que escriben las goroutines de conexión.
  - Una carrera sobre un slice/mapa puede corromper el estado o tirar un fatal error sin `recover`, y con `-H windowsgui` no queda nada en el log. Es sospechosa de crashes raros.
- **Arreglo propuesto** (el mismo patrón que `a.cfg`, AGENT.md §3.G):
  - Las escrituras con un `sync.Mutex` (o una goroutine dueña con canal de funciones); después de cada cambio se publica un `atomic.Pointer[room.State]` **inmutable** (con copia del slice). Los lectores hacen `st := a.room()` una vez por función.
  - Sacar `LastSeenUTC` por paquete fuera del `State`: un mapa dirección → `*atomic.Int64` que solo cambia al entrar o salir alguien, para no copiar el estado en cada paquete. `pingPeers` lo lee para los vencimientos.
  - `participantID`/`activeRoomName`: atómicos o dentro del mismo snapshot.
- **Tests**: `go test -race -tags nolibopusfile ./...` limpio. A mano: un build con `-race` (funciona con CGO) y una sesión de 3 instancias con migración, sin reportes.
- **Hecho cuando**: `grep -n "a.roomState" internal/ui` solo aparece dentro de los helpers del dueño.

### R3. El updater sale sin cerrar la sala

- [ ] **Problema.** `ApplyUpdate` ([updater.go](internal/updater/updater.go) ~205) lanza el exe nuevo y hace `os.Exit(0)` ahí mismo. Se saltea todo `quit` (AGENT.md §3.G "Shutdown"): no sale el `RoomLeave` (los demás esperan 20 s y migran), no se borra el mapeo UPnP (queda 8 h), no corre `flushConfig` (se pierde una config pendiente) y el log no tiene `=== cierre`. Además el proceso nuevo arranca mientras el viejo todavía tiene el puerto UDP (con `SO_EXCLUSIVEADDRUSE`) y el del celular.
- **Arreglo.** Separar en `ApplyUpdate` (bajar, verificar y renombrar; devuelve error) y `Relaunch()`. La UI llama a `a.quit(func() { updater.Relaunch() })` para que la limpieza corra antes. Opcional: el proceso nuevo recibe `--wait-pid <pid>` y espera a que termine el viejo antes de abrir puertos.
- **Tests**: a mano, actualizar estando en una sala con otra instancia: la otra ve "se desconectó" enseguida, y el router no queda con la regla.

### R4. Sacar el `PacketHello` viejo (kind 2)

- [ ] Desde Opus no hay compatibilidad con versiones viejas, y nadie manda `PacketHello` (`grep -rn "PacketHello\b"` solo encuentra los `case`). Borrar `handleLegacyHello` ([network.go](internal/ui/network.go) ~282) y los dos `case transport.PacketHello` (~111 y ~176). Revisar si `a.peerNames`/`addPeer` quedan sin uso. Dejar la constante en [udp.go](internal/transport/udp.go) comentada como reservada (no reusar el número).

### Otros (sueltos)

- [ ] **Data races**: ver R1 (y T1 para `-race` en CI).
- [ ] **`selectFile`** ([file_windows.go](internal/ui/file_windows.go)): no devolver al pool el hilo bloqueado después de `GetOpenFileNameW` (queda con ventanas IME/shell que nunca procesan mensajes). Correr el diálogo en una goroutine propia que termine sin `UnlockOSThread`.
- [ ] Revisar los dos avisos de `go vet` (uso dudoso de `unsafe.Pointer`) en [file_windows.go](internal/ui/file_windows.go) (~líneas 162 y 206).
- [ ] `syscall.NewLazyDLL` → `golang.org/x/sys/windows.NewLazySystemDLL` (carga solo desde System32).

## 1.3 Herramientas y estándares

### T1. CI que corre los tests

- [ ] **Problema.** [release.yml](.github/workflows/release.yml) solo compila cuando se pushea un tag: no hay tests en ningún workflow. Además usa `go-version: '1.24'` mientras [go.mod](go.mod) dice `go 1.26` (anda porque baja el toolchain solo, pero es más lento y frágil).
- **Arreglo.**
  - `ci.yml` nuevo (push y PR): `windows-latest` + `msys2/setup-msys2` UCRT64 con `gcc`, `opus` y `pkgconf` (como release.yml), `CGO_ENABLED=1`, y después `gofmt -l ./internal ./cmd` vacío, `go vet -tags nolibopusfile ./...`, `go test -race -tags nolibopusfile ./...` y `govulncheck ./...`.
  - En release.yml: `go-version-file: go.mod`, y los mismos tests antes del build (que no se publique un release con tests rotos). Con S7, el paso que genera `voxmesh.exe.sha256`.
  - Después: `golangci-lint` v2 (`govet`, `staticcheck`, `errcheck`, `gosec`) con una config que arranque permisiva.
- **Ojo**: el primer build CGO tarda varios minutos (Fyne + malgo). Cachear `~\go\pkg\mod` y el cache de build.

### Otros (sueltos)

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
- [x] **UPnP**: puertos externos distintos pero siempre hacia el puerto del socket, se anuncia el que funcionó, lease de 8 h sin renovar (el router rechaza re-agregar la regla) y al cerrar la sala o la app se borra el mapeado.
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
- [x] **Medidor que dice qué pasa con tu voz**: gris silencio · amarillo hay sonido pero no se manda · verde se manda · rojo modo grito, con leyenda. El medidor corre la cadena de envío también fuera de la sala, así muestra lo que pasaría. "SATURA" queda como aviso de texto en rojo.
- [x] ~~Puerta de ruido activada por defecto en −50 dBFS~~ → los defaults de entrada pasaron a ser el perfil "Personalizado" que probó Coffee (26/10): modo grito desde −5.5 dBFS (sigue entrando también al saturar), puerta de ruido **apagada** (−42 dBFS guardado). La barra post-filtro, con su línea, queda siempre visible.
- [x] **Líneas de umbral y barras de detalle**: línea roja del modo grito en la barra de entrada; con Configuración avanzada abierta, barra del VAD (cuánta voz detecta contra lo que exige) y barra post-filtro (lo que se manda) con la línea del volumen mínimo de la puerta de ruido (tenue si está apagada).
- [x] **Aro rojo en el avatar = modo grito** (el de los demás llega con `FlagShout` en sus paquetes de audio).
- [x] Ayudas "para humanos" del VAD, la puerta de ruido y RNNoise ("Exigencia del VAD", "Seguir mandando al callarte", "Volumen mínimo para mandar").
- [ ] **Probar con el amigo que habla fuerte**: que actualice y elija el perfil **Default** (hoy está en "Mi configuración", igual que vos). Si en Ajustes le aparece seguido "SATURA" en rojo junto al nivel, que baje el volumen del micrófono en Windows: lo que satura antes de llegar a VoxMesh ningún filtro lo arregla. (El aro rojo del avatar es modo grito, no saturación.)

## 1.7 Pendientes vistos al cerrar el audio configurable (2026-10-24)

Contexto para otro chat: todo lo de 1.6 y la sección "Audio configurable + perfiles" está implementado y con tests, pero **nunca se miró en pantalla** y está **sin commitear**. El mapa técnico está en [AGENT.md](AGENT.md) §3.A (cadena de envío, modo grito), §3.I (tabla `config.Params`, perfiles, medidor). Build/tests: §4 (CGO + `-tags nolibopusfile`).

Recomendado hacer primero 1, 2 y 3 (ocultar), y commitear todo junto (6).

- [x] **1. "Grabar prueba" no usa la configuración del usuario.** `testInput` ([ui_settings.go](internal/ui/ui_settings.go)) llama a `audio.PrepareOutgoingPCM`, que usa `DefaultFilterSettings()` (valores de fábrica, no los de `a.conf()`) y procesa los 2 s grabados **en un solo bloque** sin `Effects` (sin estado entre frames: no hay modo grito, pre-roll, holds del VAD/puerta ni envolvente del limitador). Lo que se escucha en "Reproducir prueba" no es lo que se manda en la sala.
  - Arreglo: recorrer la grabación de a 1920 bytes (20 ms) por la misma cadena que la sala, con la config actual y un `audio.Effects` **nuevo** (no `a.effects`, ver punto 2), juntando los frames que salen (`PrepareOutgoingFrames` con `audio.InputSettings(cfg)`). El celular (`testPhoneMic`) graba crudo desde el servidor: aplicarle lo mismo antes de guardar `a.recordedInput`.
  - Test: una grabación con silencio + voz + silencio procesada frame a frame tiene que dar el mismo resultado que mandar esos frames por `prepareOutgoingFrames`.
- [x] **2. El estado de la cadena de envío pasa del medidor de Ajustes a la sala.** `a.effects` (y `a.voiceProcessor`, RNNoise) es uno solo para toda la App ([ui_main.go](internal/ui/ui_main.go) `New`) y lo usan el medidor fuera de la sala (`meterFrame`), "Escuchar micrófono" y la sala. Al conectar, `startAudio` ([audio_bridge.go](internal/ui/audio_bridge.go)) recrea el encoder pero no la cadena: el pre-roll (los últimos 40 ms con la compuerta cerrada), la línea de retardo del modo grito (`dryDelay`, 20 ms), la mezcla en curso, los holds y la envolvente del limitador quedan de cuando estaba abierto Ajustes. Lo primero que se dice en la sala puede salir precedido por ~40 ms de audio viejo (un "clic" o un pedacito de ruido).
  - Arreglo: `func (e *Effects) Reset()` en [effects.go](internal/audio/effects.go) que, bajo `e.mu`, vuelva todo el estado a cero (filtros, envolventes, holds, `wasOpen`, `preRollCount`, `dryDelay`/`dryPos`/`dryMix`, `shoutFrames`, `sendLimiterReady`), y llamarlo en `startAudio` antes de `audio.NewEngine`. **No** reemplazar el puntero `a.effects`: lo leen otras goroutines sin lock (sería una data race). RNNoise también arrastra 20 ms: evaluar recrearlo igual (`audio.NewRNNoise`) con un `atomic.Pointer`, o aceptar esos 20 ms. Hecho con `RNNoise.Reset` (el `Denoiser` lo trae).
  - Test: después de `Reset`, el primer frame que abre la compuerta sale sin pre-roll viejo.
- [x] **3. "Push-to-talk" se puede elegir pero no hace nada.** El radio de modo de micrófono ([ui_settings.go](internal/ui/ui_settings.go), `mode := widget.NewRadioGroup(...)`) guarda `MicMode` y nadie lo lee (AGENT.md §3.I lo dice). Confunde.
  - Corto plazo: ocultar el radio (o dejarlo deshabilitado con "próximamente"). Hecho: radio sacado, `MicMode` sigue guardado.
  - Implementarlo de verdad es otra tarea (**ahora es F2**, en 1.8): tecla configurable reusando el sistema de atajos del soundboard ([hotkey_windows.go](internal/ui/hotkey_windows.go), `publishHotkeys`), y en `sendOutgoingPCM` tratar "tecla suelta" igual que el mute (la cadena sigue corriendo, el frame no sale). El medidor tendría que mostrar amarillo "no se envía (soltá/apretá la tecla)".
- [ ] **4. Revisar en pantalla** (nunca se vio): acordeón de Configuración avanzada en las tarjetas de media ventana (que no se corten labels ni sliders), barra de perfiles (Select + 4 botones en 2×2), las tres barras del medidor (entrada con línea roja; post-filtro con línea blanca; VAD con Configuración avanzada abierta), leyendas, aviso "SATURA", y el aro rojo del avatar en sala. Probar también: Guardar como / Renombrar / Borrar perfil, Descartar después de cargar un perfil, y Guardar estando en Default con cambios (tiene que crear "Personalizado").
- [ ] **5. Pasar a Default**: vos y tu amigo quedaron en "Mi configuración" por la migración; para recibir los defaults nuevos (modo grito −5.5 dBFS / al saturar / 300 ms, puerta apagada) hay que elegir **Default** una vez en Entrada y en Salida.
- [x] **6. Commitear** todo lo de audio configurable (perfiles, tabla, modo grito, medidor, puerta por defecto) antes de seguir con otra cosa.
- [x] **7. Finales de línea mezclados (CRLF/LF).** `gofmt -l` lista ~20 archivos solo por CRLF, y así no se ve cuándo algo está mal formateado de verdad. Agregar `.gitattributes` con `*.go text eol=lf` (y `*.md text eol=lf` si se quiere), normalizar con `git add --renormalize .` en un commit aparte sin otros cambios, y después `gofmt -l ./internal ./cmd` tiene que quedar vacío.
- [x] **8. `audio.PrepareOutgoingPCMWithFilters` / `PrepareOutgoingPCM` casi sin uso** ([rnnoise.go](internal/audio/rnnoise.go)): después de la refactorización del medidor solo los usan "Grabar prueba" y tests. Si se hace el punto 1, borrarlos y ajustar los tests de [rnnoise_test.go](internal/audio/rnnoise_test.go) para que usen `PrepareOutgoingFrames`. También se borró `PreparePCM` (quedó sin uso).
- [x] **9. Bajar "Volumen máximo por usuario" no baja a quien ya estaba más alto.** `peer_volume_max` solo limita los sliders nuevos y `setPeerVolume` ([peers.go](internal/ui/peers.go)); el volumen que ya tenía un participante (en el Engine y en `config.PeerVolumes`) queda arriba del nuevo tope hasta que se toca su slider. Arreglo: al guardar Ajustes (o al cambiar el parámetro), recorrer `a.peerVolumes` y `config.PeerVolumes` y recortar al tope (`setPeerVolume` con persist), y reconstruir los sliders de los avatares (`refreshPeerAvatars` los crea con el máximo vigente).

## 1.8 Funciones nuevas

### F1. Cancelación de eco (AEC)

- [ ] **Problema.** No hay cancelación de eco (`grep` no encuentra nada de AEC en `internal/audio`). Si alguien usa parlantes en vez de auriculares, su micrófono capta las voces de la sala y las devuelve: los demás se escuchan a sí mismos con 100–300 ms de retraso. RNNoise no lo saca (para RNNoise es voz) y el VAD puede abrirse con el eco. Es de las quejas que más rápido hacen abandonar una app de voz.
- **Opciones** (verificar disponibilidad en MSYS2 UCRT64 y que se pueda linkear estático: la regla de §4 es que el exe solo dependa de DLLs del sistema):
  1. **WebRTC Audio Processing (AEC3)**, paquete `mingw-w64-ucrt-x86_64-webrtc-audio-processing` (confirmar que exista y que la versión sea ≥ 1.0, que trae AEC3). La mejor calidad: estima el retardo sola y tolera deriva. Es C++: hace falta un shim C chico para cgo. Trabaja con frames de 10 ms (480 muestras a 48 kHz): partir cada frame de 20 ms en dos.
  2. **SpeexDSP** (`speex_echo_cancellation`, paquete `speexdsp`): C puro, fácil de envolver, peor con deriva y no linealidades, y necesita el retardo más o menos alineado (habría que estimarlo nosotros).
  3. Voice Capture DSP de Windows (modo comunicaciones de WASAPI): obliga a capturar por fuera de malgo y ata los dispositivos de entrada y salida. No recomendado.
  - **Recomendación**: AEC3 si el paquete existe y linkea estático; si no, SpeexDSP + estimación de retardo por correlación.
- **Diseño.**
  - **Señal de referencia** = lo que sale por los parlantes: la salida de `Engine.mix` ([engine.go](internal/audio/engine.go) ~349) después de `mixLimiter` + `softClip`. Copiarla en un ring buffer SPSC preasignado, sin asignaciones ni locks en el callback (AGENT.md §3.G "Never block the malgo callbacks"), con su hora. La goroutine de envío toma los frames de referencia que corresponden antes de procesar cada frame capturado.
  - **Relojes**: captura y reproducción son dos dispositivos malgo separados (engine.go ~189/~211), con relojes y buffers propios. El retardo es desconocido y deriva: pasarle a AEC3 una estimación inicial (`set_stream_delay_ms`) con los tamaños de período de malgo.
  - **Lugar en la cadena** (AGENT.md §3.A): primero de todo sobre el frame capturado, antes de la ganancia y de RNNoise (el AEC necesita la señal lineal; RNNoise después limpia el residuo). La línea de retardo seca del modo grito (`shout.go`) tiene que tomar la señal **ya sin eco**, o el eco vuelve por `ShoutDryMix`/`RNNoiseDryMix`.
  - Micrófono del celular: llega con más retardo variable (buffer de ~100 ms). Probar si AEC3 lo sigue; si no, desactivarlo para esa entrada.
  - **Config**: fila nueva en `config.Params` (`aec_enabled`, perfil `entrada`; empezar con `false` hasta validarlo y después pasarlo a `true` en Default), mapeo en `audio/settings.go`, clave en la sección "Supresión de ruido y voz" de `inputSections`. Seguir el procedimiento de AGENT.md §3.I "Adding a setting".
  - Log: agregar una medida de eco (ERLE o "eco estimado") al `Resumen envío`.
- **Tests**: voz lejana sintética → eco simulado (retardo ~120 ms + atenuación + reverberación corta) sumado a la voz cercana. Medir ERLE (objetivo ≥ 20 dB) y que la voz cercana se mantenga cuando hablan los dos a la vez. Test de cero asignaciones en el callback con el ring buffer. Ver si sirve [loopback_test.go](internal/audio/loopback_test.go).
- **Hecho cuando**: en una llamada real con parlantes, los demás no se escuchan a sí mismos, y con auriculares no cambia nada audible.

### F2. Push-to-talk

- [ ] **Estado.** `config.MicMode` ya existe (`VoiceActivation`/`PushToTalk`, [config.go](internal/config/config.go) ~9-13) y se guarda, pero nadie lo lee; el radio de Ajustes se sacó (1.7.3).
- **Diseño.**
  - Config: `MicMode` + `PushToTalkKey` (string, mismo formato que los atajos del soundboard). Son strings: van fuera de la tabla `Params`, en `LiveSettings` (se revierten al Descartar, AGENT.md §3.I). Opcional: `ptt_release_ms` en `Params` (cola después de soltar, ~200 ms, para no cortar la última sílaba).
  - Tecla: el poller de [hotkey_windows.go](internal/ui/hotkey_windows.go) (GetAsyncKeyState sobre el snapshot de `publishHotkeys`) tiene que reportar **mantenida** (bajada/subida), no solo el flanco → `a.pttHeld atomic.Bool`. Es global: anda con un juego en primer plano. Como el soundboard, ignorarla mientras se escribe en la app (`isTypingInApp`) y sugerir por defecto una tecla que no sea una letra.
  - Envío (`sendOutgoingPCM`, [network_audio.go](internal/ui/network_audio.go)): con `MicMode == ptt` y la tecla suelta (más la cola), tratar el frame **igual que el mute** (AGENT.md §3.E2: la cadena sigue corriendo, el frame no sale, la secuencia no avanza). Mientras está apretada, la compuerta se fuerza abierta (como el modo grito fuerza el VAD) y RNNoise sigue activo. El mute tiene prioridad.
  - UI: volver a poner el radio en Ajustes (Activación por voz / Pulsar para hablar) + botón para asignar la tecla (reusar el diálogo de atajos del soundboard). El medidor en amarillo dice "no se envía (mantené <tecla>)". Sonido corto al apretar y soltar, si están activados los sonidos de eventos.
- **Tests**: la decisión `shouldSend(muted, mode, held, releasedAt, now)` como función pura; parseo de la tecla en el snapshot.
- **Docs**: AGENT.md (sección nueva junto a §3.E2, y sacar "push-to-talk not implemented" de §3.I) y README.

### F3. Entrar a una sala con un link (sin copiar `IP:puerto`)

- [ ] **Dependencias**: se puede hacer antes de S4 (solo con la dirección), pero el link completo lleva la clave de S4. Lanzarlos juntos evita cambiar el formato dos veces.
- **Hoy**: el host aprieta "Copiar dirección" ([ui_components.go](internal/ui/ui_components.go) ~122) y pasa `IP:puerto` por otro lado; el cliente lo pega en la entrada y aprieta "Conectar". El nombre de la sala se lo manda el host en el `RoomState`.
- **Diseño.**
  - Formato `voxmesh://unirse?d=<ip:puerto>&k=<clave base64url>` (`k` con S4). Para los chats que no hacen clickeable un esquema propio, también un **código de invitación** para pegar en la misma entrada (por ejemplo `VM1-<base32>`). La entrada acepta las tres cosas: `IP:puerto`, link o código.
  - Paquete chico nuevo `internal/invite` (sin CGO): `Format`/`Parse` con tests de ida y vuelta y de entradas inválidas.
  - "Copiar dirección" pasa a "Copiar invitación".
  - Registrar el esquema en `HKCU\Software\Classes\voxmesh` (`URL Protocol`, `shell\open\command = "<exe>" "%1"`) al arrancar, sin pedir admin, actualizando la ruta si el exe se movió (`golang.org/x/sys/windows/registry`; `x/sys` ya es dependencia).
  - **Instancia única**: el link abre otro `voxmesh.exe`, que tiene que pasarle el link a la instancia que ya está abierta y salir (el puerto es exclusivo). Mutex con nombre `Local\VoxMesh` + named pipe (o `WM_COPYDATA` a la ventana). La instancia abierta pregunta "¿Unirte a la sala en X?" (si ya está en una sala, avisa que va a salir de esa).
  - `main.go`: leer `os.Args` antes de levantar la UI.
  - Opcional: QR (ya está `skip2/go-qrcode` para el celular).
  - Aviso en la UI: el link con clave es como una invitación de Discord, quien lo tenga entra.
- **Docs**: README (cómo invitar) y AGENT.md (§3.E3 o sección nueva).

## 1.9 Detalles

- [ ] **C1. Sufijo `_PUTO` → ` (2)`, ` (3)`…** en los nombres repetidos (`handleRoomHello`, [network.go](internal/ui/network.go) ~222-238; el de `handleLegacyHello` se va con R4). Extraer `uniqueName(existentes, nombre)` como función pura con test. El mensaje al cliente renombrado ("Tu nombre de usuario se cambió a…", ~404) sigue igual. Actualizar AGENT.md §3.E.
- [x] **C2. README reescrito** (2026-10-26): estaba muy desactualizado (decía que no había Opus, ni compresor, ni limitador; failover a 6 s; imágenes en un solo paquete de 48 KiB; puerto efímero).
  - Hecho: README nuevo, orientado a usuario + build. El detalle técnico queda en AGENT.md.
- [x] **C3. AGENT.md al día** (2026-10-26): versión de Go 1.26, release actual v0.17.0, archivos que faltaban en el mapa (`assets`, `level_bar`, `logs_*`), sección de seguridad (§K) con el modelo actual y punteros a S1–S9.

# 2. Más adelante

- [ ] **Rendezvous / STUN propio** (para CGNAT/NAT simétrico, ver 1.5): hoy la IP pública sale de `api.ipify.org` y el puerto se supone igual al local o al de UPnP. Un STUN público alcanza para conocer el mapeo real; para el caso "NAT simétrico de los dos lados" haría falta un relay (TURN) o un servidor chico propio. Evaluarlo solo si los amigos lo necesitan de verdad.

- [ ] **Mover `voxmesh.json` a `%AppData%\VoxMesh\`** (junto a `salas\` y `perfiles\`), para que la app sea solo el `.exe`. Migración: si existe el json al lado del exe y no en AppData, moverlo. Revisar [config.Path](internal/config/config.go), el updater (no tiene que tocar el json) y el README. Ojo con la migración de perfiles de `ui.New`: decide "había config" mirando `cfgPath`, tiene que mirar la ruta nueva y la vieja.
