# AGENDA VoxMesh

## Visión (leer primero)

VoxMesh es **un Discord simple para jugar con los pibes**, no una plataforma pública. El flujo que tiene que andar siempre:

> Creo la sala → copio la invitación → la paso por WhatsApp → la pegan → estamos en llamada, hablando mientras jugamos y jodiendo con la botonera.

Criterios para decidir qué se hace:
- **Seguro contra desconocidos de internet**: alguien que leyó el código y conoce o escanea la `IP:puerto` no tiene que poder ejecutar nada en la PC, tocar archivos, escuchar la sala ni meterse. No hace falta protegerse de los propios amigos.
- **Simple y que funcione**: nada de features "por las dudas". Cada cosa de más es un lugar más donde aparecen errores. Lo que no aporta al flujo de arriba va a "Descartado / quizás".
- **Sin compatibilidad hacia atrás**: una versión nueva no tiene que hablar con la anterior ni leer datos viejos (salas, historial, manifests, config, perfiles). Hoy existe una sola sala real. Se borra lo viejo sin transición, sin shims y sin migraciones; si un dato viejo no carga, se ignora. **Única excepción**: el updater de la versión instalada tiene que poder bajar e instalar la nueva.
- Principalmente por internet. LAN/VPN (Tailscale, ZeroTier, Radmin) también tiene que funcionar, porque es la forma de usarla sin abrir puertos.

## Cómo usar esta agenda (leer antes de tomar una tarea)

Cada tarea tiene un **ID** (`S7`, `F1`...) y está escrita para que un chat nuevo la pueda resolver sin el contexto de otros chats. Regla: **un chat = una tarea (o el grupo que diga la tabla)**.

1. Leer [AGENT.md](AGENT.md) completo (mapa técnico, reglas de hilos, build con CGO) y la tarea entera, incluidas sus "Dependencias".
2. Antes de tocar código, verificar que lo que describe la tarea sigue siendo cierto (los números de línea se corren: buscar por nombre de función). Proponer un plan corto y esperar el OK.
3. No tocar archivos ni comportamientos fuera del alcance. Si aparece otro problema, anotarlo acá con un ID nuevo en vez de arreglarlo de paso.
4. Build y tests **siempre con CGO** (AGENT.md §4). Sin eso `internal/ui` y el Engine no compilan y los tests "pasan" vacíos:
   ```
   export PATH="/c/msys64/ucrt64/bin:$PATH" CC=/c/msys64/ucrt64/bin/gcc.exe CGO_ENABLED=1
   gofmt -l ./internal ./cmd                 # vacío
   go vet -tags nolibopusfile ./...          # sin avisos
   go test -count=1 -tags nolibopusfile ./...
   ```
   Sin `ucrt64\bin` en el PATH, los tests de `audio`, `phonemic` y `ui` fallan con `0xc0000135` (DLL no encontrada).
5. Al terminar: marcar `[x]`, dejar una línea **"Hecho:"** (archivos/funciones y qué quedó afuera) y **actualizar AGENT.md** si cambió una regla, un protocolo o la estructura. Si cambió algo visible para el usuario, también [README.md](README.md) (para principiantes) y/o [DESARROLLO.md](DESARROLLO.md) (detalle técnico).
6. Un commit por tarea, mensaje en castellano. No commitear si los tests fallan.

### Orden

| # | ID | Qué, en simple | Tamaño |
|---|----|----------------|--------|
| 1 | **S7 + R3** | La actualización solo instala el exe publicado (hash y tamaño) y antes cierra bien la sala | Chico |
| 2 | **R4 + S8 + R5** | Borrar el `PacketHello` viejo, no mandar la ruta local (usuario de Windows) en las imágenes y sacar los shims de compatibilidad con versiones o datos viejos | Chico |
| 3 | **S5** | Una imagen trucada no cuelga la app y abrir una grande no congela la ventana | Chico |
| 4 | **S2** (reducido) | Sacar de la lista a los desconectados viejos (hoy crece con cada reconexión) | Chico |
| 5 | **S4 + F3** | Clave por sesión + código de invitación para pegar. Sin el código no entra nadie | Grande. Rompe compatibilidad (no importa: todos actualizan) |
| 6 | **F1** | Cancelación de eco (AEC3) para el que usa parlantes o la tele | Grande. Primero comprobar que se puede linkear |
| 7 | **T1** (mínimo) | GitHub corre vet + tests en cada push | Chico. Se puede adelantar |

Solo si aparece el problema: **R1** (data race del estado de la sala, ver "Si aparece").

---

# 0. Investigación abierta

- [ ] **"Reinicio" del botón en la barra de tareas** (la app sigue: audio y sala no se cortan). Pasa solo, sin tocar nada, en menos de 10 min (22/10 17:34–17:37, pid 16400).
  - El watchdog **nunca** registró un bloqueo de la goroutine 1 en esas sesiones. El hilo de Fyne siguió atendiendo `fyne.Do` y, en el mismo loop, `pollEvents`.
  - 21/10 20:26:59: el Visor de eventos tiene un `Application Hang` (1002) de `voxmesh.exe` pid 7172 ("Top level window is idle"). Esa sesión no tiene `=== cierre` en el log. El 22/10 no hubo evento de hang.
  - Hipótesis descartada para el caso del 22/10 (no se abrió el selector de archivos): hilo de `selectFile` devuelto al pool con ventanas (IME/shell) vivas, ver "Sueltos" (arreglado el 2026-11-02).
  - **Reproducido (22/10, monitor externo pasivo):** minimizada y sin tocarla, a los ~3,5 s Windows la marca colgada (`IsHungAppWindow`) dos veces seguidas (18:41:06 y 18:41:11). En ese momento el hilo de la ventana está en `Wait/UserRequest` (no `Suspended`) y **todo el proceso usó 0 ms de CPU en 3,5 s** (26 hilos). El watchdog no registra nada. Ese es el "reinicio": la ventana fantasma de Windows, que al irse vuelve a crear el botón al final de la barra.
  - Con un monitor que le manda `WM_NULL` a la ventana cada 200 ms **no pasó en más de 30 min**: algo que "despierta" al proceso lo evita.
  - ~~Power throttling de Windows~~ **descartado**: con `powercfg /powerthrottling disable` y plan "Alto rendimiento" se colgó igual (18:44:11, 12 s después de minimizar).
  - ~~Timers de Go~~ **descartado**: con `GODEBUG=asynctimerchan=1` falló igual (18:47:04).
  - Un monitor de cambios de contexto (`NtQuerySystemInformation`) muestra que el hilo de la ventana se despierta **~62 veces/s antes, durante y después del cuelgue** (18:48:57): el tick de 60 Hz llega y el hilo no está dormido. Aun así Windows lo marca colgado. O sea: el loop corre pero Windows considera que ese hilo no procesa sus mensajes.
  - Por qué el watchdog no lo ve: el `fyne.Do` del watchdog despierta a la goroutine 1 por `funcQueue` y queda conforme aunque `pollEvents` (`PeekMessage`) no corra. Tendría que medir el tiempo desde el último `pollEvents`/frame, o llamar a `IsHungAppWindow` sobre el propio HWND.
  - Sospechosos: `glfw.PollEvents` / `PeekMessage` mientras está minimizada, el driver de NVIDIA (ventanas ocultas `NVOpenGLPbuffer`) o un mensaje que queda en la cola sin poder sacarse.
  - Próximas pruebas:
    1. Una app Fyne mínima (Go 1.26 y Fyne 2.6.3) fuera del repo. Si también pasa, es Fyne/GLFW/driver, no VoxMesh. Después, GLFW pelado.
    2. Instrumentar: hora del último `pollEvents` y `GetQueueStatus(QS_ALLINPUT)` cuando el propio HWND da `IsHungAppWindow`.
    3. Probar sin minimizar (otra ventana encima o un juego en pantalla completa) y en una sala con audio.
    4. Los dos `AppHang` del 24/09 (`C:\ProgramData\Microsoft\Windows\WER\ReportArchive`) ya cargaban `nvoglv64.dll`: probablemente pasa desde las primeras versiones.
- [ ] **Crashes del amigo (cliente)**: pedirle el final del log de la sesión sin `=== cierre` y el "Error de aplicación" del Visor de eventos. Su log mostraba bloqueos de UI con la goroutine 1 `[runnable]` (la CPU ocupada). El watchdog nuevo ya registra CPUs, heap, GC y las goroutines que ocupan CPU. Desde el cambio del lease UPnP (1 h → 8 h) **no hubo más crashes**: si vuelven, ver R1.
- [ ] **Cortes de audio**: el audio quedó muy bien. Si alguien vuelve a notar cortes, medir con log nivel 1:
  - Quien escucha: `cortes=N (X ms)` por emisor en la línea `Audio de …` (`frases` son pausas, no cortes).
  - Quien habla: `Envío: capturados=… (50.0/s) descartados=… cola máx=…`. Menos de 50/s, descartados o cola alta = su PC o micrófono pierde audio (típico del Bluetooth).
  - Con nivel 2 queda `%AppData%\VoxMesh\llegadas-*.csv`: `VOXMESH_ARRIVALS=<csv> go test -run TestJitterReplayArrivals -v ./internal/audio/` reproduce esa red por el jitter buffer.

# 1. Tareas (en el orden de la tabla)

**Situación de seguridad (2026-10-28).** El host expone un puerto UDP a internet (UPnP), pero desde S4 + F3 todo datagrama va cifrado y autenticado con la clave de la sesión, que solo viaja en la invitación: sin ella no se puede escuchar ni hacer nada. El lado cliente además solo acepta audio, chat, archivos y estado de **su host** (`handleClientPacket` + `Failover.Seen`). Cerrado también que alguien escriba, lea o borre archivos (S1, S10, S11), y el updater solo instala el exe publicado (S7). Los filtros contra desconocidos de S2/S3 quedan en "Descartado / quizás".

Sobre el puerto UPnP: la app borra el mapeo al cerrar la sala o la app. Si se cierra mal, el lease dura 8 h, pero con la app cerrada el puerto no lleva a nada, y con S4 tampoco sirve sin la clave. **No hace falta tarea.**

### S7. Updater: verificar lo que se descarga

- [x] **Hecho** (2026-10-28): [updater.go](internal/updater/updater.go) `pickRelease` (nombre exacto `voxmesh.exe`, exige `voxmesh.exe.sha256`, tamaño ≥ 1 MB) e `install` (baja primero el `.sha256`, hash y tamaño exacto contra `AssetSize` mientras escribe; si no coincide borra el `.new`). [release.yml](.github/workflows/release.yml) publica `voxmesh.exe.sha256`. Tests con `httptest` en [updater_test.go](internal/updater/updater_test.go). Afuera: firma ed25519, reintentos de descarga. **El próximo release tiene que salir con los dos cambios juntos.**
- **Problema** ([updater.go](internal/updater/updater.go)). `CheckForUpdate` elige `voxmesh.exe` **o cualquier `.exe`** del release. `ApplyUpdate` solo verifica un tamaño mínimo: ni hash, ni que el tamaño coincida con `AssetSize` (se lee y no se usa). HTTPS protege el camino; falta verificar que el archivo sea el publicado.
- **Arreglo (versión simple, sin firma):**
  - Exigir el nombre exacto `voxmesh.exe`.
  - [release.yml](.github/workflows/release.yml) publica `voxmesh.exe.sha256`. El updater lo baja, compara el hash y el tamaño contra `AssetSize`, y si algo no coincide borra el `.new` y no reemplaza nada.
  - Transición: el cambio de `release.yml` y el del updater salen **en el mismo release**. Los updaters viejos no miran el hash, así que pueden instalarlo; el updater nuevo solo baja releases posteriores, que ya traen el `.sha256`.
- **Tests**: hash que no coincide → no reemplaza el exe y borra el `.new`; tamaño distinto → igual; falta el `.sha256` → no actualiza.
- **Descartado**: firma ed25519 (protege contra un repo o CI comprometidos; para este uso alcanza con HTTPS + hash).

### R3. El updater sale sin cerrar la sala

- [x] **Hecho** (2026-10-28): `ApplyUpdate` solo baja, verifica y renombra; `updater.Relaunch` lanza el exe con `--wait-pid`; `updater.WaitForParent` (primera línea de [main.go](cmd/voxmesh/main.go), tope 15 s). `executeAutoUpdate` sale por `a.quit(cleanup, relaunch)` ([ui_main.go](internal/ui/ui_main.go)), y `quit` ganó el parámetro `after`. Prueba manual hecha el 2026-11-02 (ver "Historial").
- **Problema.** `ApplyUpdate` lanza el exe nuevo y hace `os.Exit(0)` ahí mismo. Se saltea todo `quit` (AGENT.md §3.G "Shutdown"): no sale el `RoomLeave` (los demás esperan 20 s y migran), no se borra el mapeo UPnP, no corre `flushConfig` y el log no tiene `=== cierre`. Además el proceso nuevo arranca mientras el viejo todavía tiene el puerto UDP (`SO_EXCLUSIVEADDRUSE`) y el del celular.
- **Arreglo.** Separar en `ApplyUpdate` (bajar, verificar y renombrar; devuelve error) y `Relaunch()`. La UI llama a `a.quit(func() { updater.Relaunch() })` para que la limpieza corra antes. El proceso nuevo recibe `--wait-pid <pid>` y espera a que termine el viejo antes de abrir puertos.
- **Tests**: a mano, actualizar estando en una sala con otra instancia: la otra ve "se desconectó" enseguida y el router no queda con la regla.

### R4. Sacar el `PacketHello` viejo (kind 2)

- [x] **Hecho** (2026-10-28): borrados `PacketHello` y su nombre en `PacketKindName` ([udp.go](internal/transport/udp.go)), los dos `case` y `handleLegacyHello` ([network.go](internal/ui/network.go)), `addPeer` y, en `updatePeerLag`, la búsqueda por `"(dirección)"` en el nombre (formato del commit inicial) ([peers.go](internal/ui/peers.go)). `a.peerNames` queda: es la lista de avatares. Los demás kinds no se renumeran.
- Desde Opus no hay compatibilidad con versiones viejas y nadie manda `PacketHello` (`grep -rn "PacketHello\b"` solo encuentra los `case`). Borrar `handleLegacyHello` ([network.go](internal/ui/network.go)), los dos `case transport.PacketHello` y la constante en [udp.go](internal/transport/udp.go) (sin compatibilidad no hace falta reservar el número). Borrar también `a.peerNames`/`addPeer` si quedan sin uso.

### S8. No mandar la ruta local de las imágenes

- [x] **Hecho** (2026-10-28): sin el campo en `history.Message` ni en los dos envíos de [ui_chat.go](internal/ui/ui_chat.go). Test `TestImageMessageHasNoLocalPath` ([history_test.go](internal/history/history_test.go)).
- `history.Message.ImagePath` (`json:"image_path"`) lleva la ruta absoluta local (`C:\Users\<usuario de Windows>\AppData\…`) y viaja a la sala con cada imagen ([ui_chat.go](internal/ui/ui_chat.go), los dos envíos). Nadie la lee: el receptor la recalcula con `ImagePath(id, ext)`. Arreglo: **sacar el campo** de `Message` y de los dos envíos (verificar con `grep -rn "\.ImagePath\b"` que nada lo lea; ojo, `Store.ImagePath(id, ext)` es otra cosa y sí se usa). Los historiales viejos que lo tengan no necesitan nada: `encoding/json` ignora campos desconocidos.

### R5. Sacar los shims de compatibilidad con versiones o datos viejos

- [x] **Hecho** (2026-10-28): borrados `config.MigrateToProfiles` + `MigratedProfileName` ([profiles.go](internal/config/profiles.go)) y el "había config" de `ui.New` ([ui_main.go](internal/ui/ui_main.go)); sonidos solo `.pcm` y con extensión obligatoria (sin `defaultSoundExtension`, [history.go](internal/history/history.go)); `audio.PickDevice` solo por ID, sin el fallback por nombre de las configs sin ID ([device_pick.go](internal/audio/device_pick.go)). Tests ajustados (`TestStoreNormalizesExtensions`, `TestPickDevice`, `deviceOptions`; fuera `TestMigrateToProfiles`). Afuera a propósito: `log_level` mínimo 1 y la tolerancia de los perfiles (rango), `LiveSettings` plano (es el formato actual) y la importación de `.wav`/`.mp3` (entrada del usuario, se guarda `.pcm`).
- **Por qué**: criterio "Sin compatibilidad hacia atrás" de la Visión. Cada shim es código que nadie prueba.
- **Primero relevar, después borrar**: buscar (`grep -rni "viejo\|legacy\|migra\|compat\|antes"` en `internal/` y `cmd/`, más lo que diga AGENT.md) y listar cada shim con archivo/función antes de tocar nada. Conocidos:
  - `config.MigrateToProfiles` (crea "Mi configuración" desde un `voxmesh.json` anterior a los perfiles, `ui.New`) y la lógica de "había config" que lo acompaña.
  - Sonidos sin extensión = `.wav` (`defaultSoundExtension` en [history.go](internal/history/history.go)) y `.wav` en la lista blanca, que existen por manifests viejos y por el default de `SaveSound`. La app solo produce `.pcm`: dejar solo `.pcm` y exigir la extensión.
  - Lo que cubren R4 (`PacketHello`) y S8 (`image_path`).
- **No es shim (no tocar)**: lo que protege contra datos inválidos de la red (S1/S10/S11), la tolerancia de los perfiles a claves faltantes o fuera de rango (sirve entre versiones nuevas, AGENT.md §3.I) y lo que necesita el updater para instalar la versión nueva.
- **Tests**: ajustar los que cubrían los shims (por ejemplo `TestStoreNormalizesExtensions` con `.wav`) en vez de dejarlos pasando con código muerto.

### S5. Imágenes: límite de dimensiones y nada de decodificar en el hilo de UI

- [x] **Hecho** (2026-10-28): paquete [imagefile](internal/imagefile/imagefile.go) sin CGO: `Check` (solo `image.DecodeConfig`; png/jpeg/gif/webp, tope **40 MP y 16384 px por lado**) y `Load` (cabecera → decode → reducir; reemplaza a `loadThumbnail`). Al recibir, `handleAssetChunk` no guarda lo que no pasa `Check` ([network_sync.go](internal/ui/network_sync.go)); al enviar, `imageSendable` en `sendChatImageBytes`/`sendImageFile` ([ui_chat.go](internal/ui/ui_chat.go)). `showLightbox` ([ui_components.go](internal/ui/ui_components.go)) decodifica fuera del hilo de UI a tamaño de pantalla, de a una (`lightboxDecode`), con "Cargando imagen…" y descarta el resultado si se cerró. Tests en [imagefile_test.go](internal/imagefile/imagefile_test.go) (cabecera gigante sin reservar memoria, topes, no-imágenes, una imagen por extensión de la lista blanca). Afuera: los chunks que reenvía el host sin validar (cada receptor descarta), deduplicar/acotar miniaturas (R2) y las imágenes viejas en disco que no pasan (no se muestran, error en el log).
- **Problema.** `loadThumbnail` ([ui_chat_thumbnail.go](internal/ui/ui_chat_thumbnail.go)) hace `image.Decode` sin mirar dimensiones: un archivo chico puede declarar dimensiones enormes y reservar gigas. El lightbox ([ui_components.go](internal/ui/ui_components.go)) usa `canvas.Image{File: …}`, que decodifica la imagen completa **en el hilo de UI** (congela la ventana; AGENT.md §3.G lo prohíbe).
- **Arreglo.** `image.DecodeConfig` antes de decodificar y rechazar si pasa un tope (por ejemplo 40 MP o un lado > 16384). Validarlo también al recibir (`handleAssetChunk`) y no guardar lo que no sea una imagen válida. Lightbox: decodificar fuera del hilo de UI a tamaño de pantalla (reusar `loadThumbnail` con otro máximo) y mostrar el resultado con `fyne.Do`.
- **Tests**: una cabecera PNG que declara dimensiones gigantes → error sin reservar memoria (solo se lee la cabecera).

### S2. Lista de participantes con poda (versión reducida)

- [x] **Hecho** (2026-10-28): en [state.go](internal/room/state.go), `PruneDisconnected` (desconectados hace más de `DisconnectedTTL` = **10 min**; nunca el host ni un conectado), `MakeRoom` (con `MaxParticipants` = **16** poda a los desconectados más viejos, nunca a uno de menos de `participantTimeout` = 20 s) y `NextOrder` (max+1: con huecos, `len` repetía un `Order` y `Validate` fallaba). En [network.go](internal/ui/network.go), `pingPeers` → `pruneDisconnected` cada 2 s (poda + `RemovePeer` + `broadcastRoomState`). En `handleRoomHello`, un ID nuevo pasa por `MakeRoom` y, si sigue lleno, `rejectRoomFull` (`PacketRoomLeave` con `room.RoomFullMessage`): el cliente lo acepta solo de su host y antes de entrar (`Failover.Refused` → `ActGiveUp` "La sala está llena (máximo 16 participantes)."). Un ID conocido que vuelve desde otra dirección: `RemovePeer` de la vieja. `removePeerAddress` no saca una dirección que tiene un participante conectado. Tests en [state_test.go](internal/room/state_test.go) (poda, tope, host, `NextOrder`, TTL > `Announce`, sala llena < 8 KB) y [failover_test.go](internal/room/failover_test.go) (`Refused`). Afuera: S4 (`AddPeer` en cada paquete vuelve a sumar a un desconectado que sigue mandando), S12, R6 y C2 (abajo).
- **Problema.** `room.State.Participants` no tiene tope ni poda: cada `RoomHello` con un ID nuevo agrega uno. Pasa sin mala intención: `ParticipantID = sha256(usuario|dirección)`, así que reconectar desde otro puerto crea un ID nuevo y el viejo queda como "desconectado" para siempre. El `RoomState` viaja en un solo datagrama: si la lista crece mucho, deja de entrar. Además los peers de difusión no se sacan siempre (`RemovePeer` solo en `handleParticipantLeave` y al pasar a cliente).
- **Arreglo.**
  - Poda de desconectados viejos (por ejemplo > 10 min; `Candidates()` ya ignora desconectados) en `pingPeers` o en el `State`, con un tope de participantes (por ejemplo 16) como red de seguridad.
  - `RemovePeer` cuando un participante se va, vence, se poda o cambia de dirección.
  - No romper la migración: Ping, Pong y RoomState de direcciones que figuran en la lista aunque estén desconectadas se siguen atendiendo (revisar `takeOverHost` y [failover.go](internal/room/failover.go)).
- **Tests**: poda y tope como funciones del `State`. A mano: migración (ver "Pruebas manuales").
- **Fuera de alcance (lo cubre S4)**: filtrar paquetes de direcciones desconocidas, `AddPeer` solo al aceptar `RoomHello`, no aceptar `SystemMessage` de clientes, `sync_request` solo del host, límite de `RoomHello`. Si S4 se demora mucho, retomarlo de "Descartado / quizás".

### S4. Clave por sesión + cifrado de los datagramas

- [x] **Hecho** (2026-10-28): [crypto.go](internal/transport/crypto.go) (`Key`, `NewKey`, `roomCipher.seal`/`open`: AES-256-GCM, cabecera de 16 bytes como AAD, nonce = 4 bytes aleatorios por socket + contador de 64 bits) y [udp.go](internal/transport/udp.go) (magic `VM02`, `Listen(addr, key)`, `Send`/`Broadcast` sellan con buffers de un `sync.Pool`, `bulkLoop` con buffer propio, `Receive` abre y descarta lo que no abre contando en `Invalid`/`LastInvalid`, `Relay`/`TryRelayBulk` reenvían los mismos bytes). [ui_connection.go](internal/ui/ui_connection.go): `openHost` genera la clave en cada "Iniciar sala". [network.go](internal/ui/network.go): el host reenvía audio y chat con `Relay` y los chunks con `TryRelayBulk`. [network_failover.go](internal/ui/network_failover.go): `rejectedPackets` loguea los descartados (nivel 1, una línea / 10 s). [failover.go](internal/room/failover.go): antes del primer estado, `RoomHello` cada 2 s y a los `Join` (10 s) `ActGiveUp` "Código incorrecto o host inalcanzable.". Tests: [crypto_test.go](internal/transport/crypto_test.go) (ida y vuelta, cualquier byte alterado, clave equivocada, nonces sin repetir, 0 asignaciones en `seal`/`open`, `Receive` descarta otra clave, `Relay` y `TryRelayBulk` con los mismos bytes) y `TestFailover_Join*`. Afuera: ventana anti-repetición, guardar la clave en disco, micrófono del celular (socket y token propios). Prueba manual hecha el 2026-11-02 (ver "Historial").
- **Dependencias**: va **en el mismo chat y el mismo release que F3** (la clave viaja en la invitación). Rompe compatibilidad con versiones anteriores: no importa (Visión), sin transición.
- **Qué logra**: sin la invitación nadie puede escuchar, mandar paquetes válidos, echar gente ni robar el host, aunque sepa la `IP:puerto` y haya leído el código.
- **Diseño (simple):**
  - **Clave por sesión**: 32 bytes aleatorios (`crypto/rand`) generados en cada "Iniciar sala", solo en memoria. No se guarda en disco: como la IP pública cambia, la invitación se vuelve a pasar en cada sesión igual. Nueva sesión = clave nueva, así que no hace falta "regenerar si se filtra".
  - Los clientes la toman de la invitación y la tienen en memoria mientras dure la sesión.
  - **AES-256-GCM de la stdlib** (sin dependencias, AES-NI en cualquier x64). Los 16 bytes de cabecera van en claro como AAD; el payload lleva nonce + texto cifrado + tag (+28 bytes: el audio pasa de ~130 a ~158 bytes, unos +11 kbps por emisor, CPU despreciable).
  - Nonce de 12 bytes: 4 aleatorios por socket + contador de 64 bits (nunca se repite con la misma clave).
  - El host verifica antes de reenviar y reenvía los mismos bytes (todos comparten la clave: no hay que volver a cifrar).
  - Magic nuevo `VM02`. Un paquete que no se puede abrir se descarta en silencio, con un contador logueado a nivel 1 (máximo una línea cada 10 s, como `rejectedAssets` en [network_sync.go](internal/ui/network_sync.go)). El cliente con un código equivocado ve "Código incorrecto o host inalcanzable" después del timeout.
  - **Sin ventana anti-repetición**: aprovecharla requiere estar en el medio de la red; se acepta. El audio ya descarta duplicados y tardíos en el jitter buffer.
  - El micrófono del celular no cambia (es LAN y tiene su propio token).
- **Host y migración (comportamiento esperado):**
  - Si el host se va, los demás **siguen con la misma clave sin link nuevo**: todos la tienen y la migración elige el mismo host nuevo para todos. Si el host sale bien (Salir / cerrar), avisa y migran enseguida; si se le corta internet, esperan 20 s por si vuelve (igual que hoy).
  - El host que vuelve **sin haber cerrado la app** tiene la clave y se suma solo, como hoy.
  - Si **cerró y volvió a abrir** (o cualquier participante que cerró), pide la invitación a cualquiera de la sala (ver F3).
  - La clave vale para toda la sesión aunque cambie el host. Solo cambia cuando alguien vuelve a apretar "Iniciar sala".
- **Límites aceptados**: quien tenga la invitación entra (como un link de Discord); entre miembros no hay secretos (un miembro podría falsificar paquetes de otro: es entre amigos); sin secreto hacia adelante; las IPs se siguen viendo.
- **Tests**: ida y vuelta; paquete alterado → rechazo; clave equivocada → rechazo; `Seal`/`Open` sin asignaciones en el camino del audio (buffers reutilizados). A mano: migración con la clave (ver "Pruebas manuales").
- **Docs**: AGENT.md §3.C (formato del paquete) y §K, README (cómo invitar).

### F3. Código de invitación para pegar (versión simple)

- [x] **Hecho** (2026-10-28): paquete [invite](internal/invite/invite.go) sin CGO. Código `VM1-` + base64url sin relleno de `[largo de la dirección]["IP:puerto"][clave de 32 bytes]` (~75 caracteres); `Parse` con errores para no-invitación (incluido `IP:puerto` solo), versión desconocida, truncado o base64 roto, dirección no literal o puerto 0, y clave que no mide 32. Tests en [invite_test.go](internal/invite/invite_test.go). `connectClient` solo acepta la invitación. Botón **"Invitar"** en la barra superior ([ui_invite.go](internal/ui/ui_invite.go)), para cualquier participante en sala: el cliente copia la dirección del host como la ve él; el host elige en un menú "Internet" o "LAN/VPN · <interfaz>" (`netinfo.LocalIPv4s`, incluye Tailscale/ZeroTier/Radmin). Aviso: "Quien tenga la invitación puede entrar". Sale "Copiar dirección" del panel Host. `LastPeer` sale de la config (la clave no va a disco): la invitación para volver queda en memoria (`a.rejoinInvite`) y vuelve a la entrada al salir de la sala. Afuera (descartado): `voxmesh://`, `HKCU`, instancia única, QR.
- **Dependencias**: con S4, mismo chat y mismo release.
- **Hoy**: el host aprieta "Copiar dirección" ([ui_components.go](internal/ui/ui_components.go), `endpointText()`) y pasa `IP:puerto`; el cliente lo pega en la entrada y aprieta "Conectar".
- **Diseño:**
  - Un **código de texto** con dirección + clave (por ejemplo `VM1-<base64url>`), para copiar y pegar por WhatsApp. La entrada de "Conectar" acepta el código. Sin la clave no se puede entrar, así que `IP:puerto` solo deja de servir.
  - Paquete nuevo chico `internal/invite` (sin CGO): `Format`/`Parse` con tests de ida y vuelta y de entradas inválidas (truncado, base64 roto, versión desconocida).
  - "Copiar dirección" pasa a **"Copiar invitación"**, y la puede usar **cualquier participante**, no solo el host: todos saben la clave y el host actual. Sirve para el que llega tarde después de una migración o el que cerró la app.
  - Dirección dentro del código: por defecto la pública (la de hoy). Para LAN/VPN, una forma simple de copiar la invitación con la dirección local (por ejemplo un segundo botón o un selector "Internet / LAN-VPN" que use la IP de la interfaz, incluidas las de Tailscale/ZeroTier). Si la copia un cliente, va la dirección del host tal como la ve ese cliente.
  - Aviso corto en la UI: "Quien tenga la invitación puede entrar".
- **Descartado** (lo más propenso a fallar): esquema `voxmesh://` clickeable, registro en `HKCU`, instancia única con mutex/named pipe, QR.
- **Docs**: README (cómo invitar) y AGENT.md (§3.E3 o sección nueva).

### F1. Cancelación de eco (AEC3)

- [x] **Hecho (2026-10-28):** WebRTC Audio Processing 1.3 compilada estática por [third_party/webrtc-apm/build.ps1](third_party/webrtc-apm/build.ps1) (MSYS2 solo trae la DLL; misma receta + `--default-library=static`; la llaman `build.ps1` y release.yml) y shim C ([aec_shim_windows.cc](internal/audio/aec_shim_windows.cc), [aec_windows.go](internal/audio/aec_windows.go)). Referencia: `Engine.mix` copia su salida (después de `mixLimiter` + `softClip`) a un ring SPSC con la hora de cada callback ([echo.go](internal/audio/echo.go), sin asignaciones ni locks). Entra primero de todo en `sendOutgoingPCM` (`Engine.CancelEcho`, antes de `prepareOutgoingFrames` y aunque el filtro de voz esté apagado), así RNNoise y la línea seca del modo grito ven la voz sin eco. Celular: el mismo AEC con la referencia corrida `phone_mic_buffer_ms`; AEC3 lo sigue (después de un salto de 20 ms por deriva tarda ~2 s), así que queda activado. Config `aec_enabled` (perfil entrada, apagado), `audio.InputEchoSettings`, primera fila de "Supresión de ruido y voz". Log: `eco: frames ERLE retardo` en las líneas `Envío` / `Resumen envío`. Tests ([aec_test.go](internal/audio/aec_test.go), [echo_test.go](internal/audio/echo_test.go)): ERLE 48 dB parlantes / 61 dB tele / 43–57 dB celular; hablando los dos la voz cercana pierde ≤ 1,5 dB (correlación 0,87–0,9: el supresor de AEC3 atenúa las bandas donde domina el eco); cero asignaciones en `mix` y `CancelEcho`; ring concurrente limpio con `-race`. Exe +1,1 MB, solo DLLs del sistema (suma WINMM y `api-ms-win-crt-time`). **Quedó afuera**: el AEC en "Escuchar micrófono" y "Grabar prueba" (no hay Engine de sala); ajustar la config interna de AEC3 (se usa la de WebRTC tal cual). Prueba manual hecha el 2026-11-02 (ver "Historial").
- **Por qué**: uno de los pibes a veces usa la tele con parlantes. Su micrófono capta las voces de la sala y las devuelve: los demás se escuchan a sí mismos con retraso. RNNoise no lo saca (para RNNoise es voz) y el VAD puede abrirse con el eco.
- **Dónde corre**: 100 % local, en la PC de quien usa parlantes. La activa solo él con un checkbox ("Uso parlantes: cancelar eco"), apagado por defecto. Los demás no cambian nada.
- **Opción elegida: WebRTC Audio Processing (AEC3)**. Estima el retardo sola y tolera deriva, y eso importa porque una tele por HDMI suma un retardo grande y variable.
  - **Paso 1, antes de escribir nada**: comprobar que exista el paquete MSYS2 UCRT64 `mingw-w64-ucrt-x86_64-webrtc-audio-processing` (versión ≥ 1.0, que trae AEC3) y que **linkee estático** (regla de §4: el exe solo depende de DLLs del sistema). Si no se puede, frenar y volver a decidir. La alternativa anotada es SpeexDSP (`speex_echo_cancellation`, C puro, peor con deriva, necesita estimar el retardo nosotros).
  - Es C++: hace falta un shim C chico para cgo. Trabaja con frames de 10 ms (480 muestras a 48 kHz): partir cada frame de 20 ms en dos.
  - Costo: el exe crece unos MB; unos pocos % de CPU **solo en quien lo activa**.
- **Diseño.**
  - **Señal de referencia** = lo que sale por los parlantes: la salida de `Engine.mix` ([engine.go](internal/audio/engine.go)) después de `mixLimiter` + `softClip`. Copiarla en un ring buffer SPSC preasignado, sin asignaciones ni locks en el callback (AGENT.md §3.G "Never block the malgo callbacks"), con su hora. La goroutine de envío toma los frames de referencia que corresponden antes de procesar cada frame capturado.
  - **Relojes**: captura y reproducción son dos dispositivos malgo separados, con relojes y buffers propios. Pasarle a AEC3 una estimación inicial (`set_stream_delay_ms`) con los tamaños de período de malgo.
  - **Lugar en la cadena** (AGENT.md §3.A): primero de todo sobre el frame capturado, antes de la ganancia y de RNNoise (el AEC necesita la señal lineal; RNNoise limpia el residuo). La línea de retardo seca del modo grito (`shout.go`) tiene que tomar la señal **ya sin eco**, o el eco vuelve por `ShoutDryMix`/`RNNoiseDryMix`.
  - Micrófono del celular: llega con más retardo variable (~100 ms de buffer). Probar si AEC3 lo sigue; si no, desactivarlo para esa entrada.
  - **Config**: fila nueva en `config.Params` (`aec_enabled`, perfil `entrada`, `false` por defecto), mapeo en `audio/settings.go`, clave en la sección "Supresión de ruido y voz" de `inputSections`. Seguir AGENT.md §3.I "Adding a setting".
  - Log: una medida de eco (ERLE o "eco estimado") en el `Resumen envío`.
- **Tests**: voz lejana sintética → eco simulado (retardo ~120 ms + atenuación + reverberación corta) sumado a la voz cercana. ERLE ≥ 20 dB y la voz cercana se mantiene cuando hablan los dos a la vez. Test de cero asignaciones en el callback con el ring buffer. Ver si sirve [loopback_test.go](internal/audio/loopback_test.go).
- **Hecho cuando**: con el amigo de la tele en la llamada, los demás no se escuchan a sí mismos, y con auriculares no cambia nada audible.

### T1. CI que corre los tests (mínimo)

- [ ] **Problema.** [release.yml](.github/workflows/release.yml) solo compila cuando se pushea un tag: no hay tests en ningún workflow. Además usa `go-version: '1.24'` mientras [go.mod](go.mod) dice `go 1.26`.
- **Arreglo.**
  - `ci.yml` nuevo (push y PR): `windows-latest` + `msys2/setup-msys2` UCRT64 con `gcc`, `opus` y `pkgconf` (como release.yml), `CGO_ENABLED=1`, y después `gofmt -l ./internal ./cmd` vacío, `go vet -tags nolibopusfile ./...` y `go test -race -tags nolibopusfile ./...`.
  - En release.yml: `go-version-file: go.mod` y los mismos tests antes del build (que no se publique un release con tests rotos).
  - Cachear `~\go\pkg\mod` y el cache de build (el primer build CGO tarda varios minutos).
- **Descartado**: `golangci-lint` y `govulncheck` (se pueden sumar después si hace falta).

## Si aparece

### R1. `a.roomState` con un solo dueño (data race)

- [ ] **Cuándo hacerla**: si la app se vuelve a cerrar sola. Primer paso, barato: un build con `-race` (funciona con CGO) y una sesión de 3 instancias con entradas, salidas y una migración. Si reporta algo, esta tarea; si no, sigue esperando.
- **Problema** (AGENT.md §H). `a.roomState` (nombre, `HostID`, `Epoch` y la lista de participantes) se lee y escribe sin sincronización desde varias goroutines:
  - `receiveLoop` → `handleHostPacket` ([network.go](internal/ui/network.go)): actualiza `LastSeenUTC` con **cada paquete** (incluida la voz); `handleRoomHello` agrega o reemplaza participantes; `handleParticipantLeave`, `handleMutePacket`.
  - `pingPeers`: recorre la lista cada 2 s para los vencimientos. **No tiene `recover`**: un panic ahí cierra la app sin dejar nada en el log.
  - `failoverLoop` ([network_failover.go](internal/ui/network_failover.go)): `takeOverHost`, `switchHost`, `stepDown` reemplazan el estado entero.
  - Ajustes ([ui_settings.go](internal/ui/ui_settings.go), escribe `CanBeHost`), y lectores sueltos (`a.roomState.HostID == a.participantID` en chat y botonera).
  - `a.participantID` y `a.activeRoomName` son campos comunes que escriben las goroutines de conexión.
- **Arreglo propuesto** (mismo patrón que `a.cfg`, AGENT.md §3.G): escrituras con un `sync.Mutex` y, después de cada cambio, un `atomic.Pointer[room.State]` **inmutable** (con copia del slice); lectores con `st := a.room()` una vez por función. `LastSeenUTC` por paquete fuera del `State`: un mapa dirección → `*atomic.Int64` que solo cambia al entrar o salir alguien (así no se copia el estado 50 veces por segundo). `participantID`/`activeRoomName`: atómicos o dentro del snapshot.
- **Tests**: `go test -race -tags nolibopusfile ./...` limpio y la sesión `-race` a mano sin reportes.

## Sueltos (chicos, cuando se toque esa zona)

- [x] **`selectFile`** ([file_windows.go](internal/ui/file_windows.go)): no devolver al pool el hilo bloqueado después de `GetOpenFileNameW` (queda con ventanas IME/shell que nunca procesan mensajes). Correr el diálogo en una goroutine propia que termine sin `UnlockOSThread`.
  - **Hecho** (2026-11-02): `selectFile` corre el diálogo (`openFileDialog`, que inicializa COM STA) en una goroutine que hace `LockOSThread` y termina sin `UnlockOSThread`, así Go descarta el hilo. El llamador espera el resultado por un canal. Si el diálogo entra en pánico, el pánico se repite en el llamador (con su stack) y lo sigue atrapando el `recover` de `selectChatImage`/`pickSoundFile`. Para el usuario no cambia nada.
- [x] Los dos avisos de `go vet` (uso dudoso de `unsafe.Pointer`) en [file_windows.go](internal/ui/file_windows.go) (~líneas 162 y 206).
  - **Hecho** (2026-11-02): los dos `unsafe.Slice` de `readClipboardImage` usan `osPointer(addr)`, que hace `*(*unsafe.Pointer)(unsafe.Pointer(&addr))`. Es la misma conversión, sobre la memoria de `GlobalLock` (fuera del heap de Go). `go vet -tags nolibopusfile ./...` sale sin avisos.
- [x] `syscall.NewLazyDLL` → `golang.org/x/sys/windows.NewLazySystemDLL` (carga solo desde System32).
  - **Hecho** (2026-11-02): `windows.NewLazySystemDLL` en [file_windows.go](internal/ui/file_windows.go), [hotkey_windows.go](internal/ui/hotkey_windows.go), [headless_windows.go](internal/ui/headless_windows.go) y [window_state_windows.go](internal/ui/window_state_windows.go). No había tipos `*syscall.LazyProc` declarados. `UTF16PtrFromString`/`UTF16ToString` también pasan a `x/sys/windows`, así esos archivos ya no importan `syscall` (`transport` lo sigue usando para el socket). No queda ningún `syscall.NewLazyDLL`. La regla quedó en AGENT.md §K.
- [x] **S12. Nombre de usuario sin tope**: `Participant.Username` viaja en cada `RoomState` sin límite de largo (`handleRoomHello` solo hace `TrimSpace`). Con 16 participantes y nombres de 32 runas la sala ocupa unos 4 KB, pero un nombre de miles de caracteres hace que el estado no entre en un datagrama aunque haya pocos participantes. Arreglo: `ClampUsername` (como `history.ClampRoomName`) al guardar el nombre y en `handleRoomHello`.
  - **Hecho** (2026-11-02): `room.ClampUsername` + `room.MaxUsernameRunes` = **32** runas ([state.go](internal/room/state.go)). Es el tope de Discord y Steam; la entrada de Ajustes no tenía límite. Se aplica en `saveSettings` ([ui_settings.go](internal/ui/ui_settings.go)) y en `handleRoomHello` **antes** del sufijo por nombre repetido: recortado después, el sufijo se perdería. El sufijo puede pasar de 32 runas, acotado por los 16 participantes. Test `TestClampUsername`; `TestFullRoomStateFitsInOneDatagram` usa la constante. README: "nombre (hasta 32 caracteres)". **Quedó afuera** (se acepta, sin compatibilidad hacia atrás): un nombre de más de 32 que ya esté en `voxmesh.json` sale entero si esa PC es host; como cliente, el host lo recorta, él ve una vez "Nombre en uso…" y guarda el recortado.
- [ ] **R6. Dos registros con la misma dirección**: si alguien cambia de nombre y reconecta desde el mismo puerto, el host tiene dos IDs con la misma dirección. `handleHostPacket` actualiza `LastSeenUTC` del **primero** que encuentra (el viejo), así que el nuevo vence a los 20 s; `handleParticipantLeave` busca por ID **o** dirección, da con el viejo (ya desconectado) y vuelve sin marcar nada, pero antes hizo `RemovePeer`: el cliente pierde la difusión hasta su próximo paquete, cada 2 s. Arreglo probable: en `handleRoomHello`, otro ID en la misma dirección es el mismo socket → sacarlo de la lista.
- [ ] **C2. "Nombre (2)" al reconectar desde otro puerto**: el registro viejo sigue "conectado" hasta `participantTimeout` (20 s), así que el que vuelve en menos de eso con un ID nuevo entra como "Nombre (2)".
- [ ] **R2. Chat / miniaturas**, si el chat se pone lento con muchas imágenes: `refreshChat` reconstruye todas las filas en el hilo de UI (en cada tecla del buscador y por cada imagen recibida) y lanza una decodificación por imagen sin deduplicar. Arreglo: juntar los refresh (flag sucio + un solo `fyne.Do`), cargas deduplicadas + semáforo de 1–2 decodificaciones, `thumbCache` con tope; `widget.List` virtualizado solo si hace falta.
- [x] **C4. Ajustes pide ~1000 px de ancho** (visto en C3, ya pasaba antes): con la ventana a media pantalla (~960 px), abrir Ajustes la agranda a ~1000 px y la tarjeta de la derecha y "Guardar" quedan cortadas. Sospechoso: `container.NewGridWithColumns(3, …)` de la tarjeta "Interfaz y Sistema" ([ui_settings.go](internal/ui/ui_settings.go)), donde cada columna mide lo que "Abrir carpeta de datos y grabaciones". Arreglo probable: esos tres botones en dos filas o en un VBox.
  - **Hecho** (2026-11-02): además de esos tres botones (ahora uno debajo del otro), lo ensanchaban los textos de los medidores, que cambian solos ("Nivel: … · SATURA (bajá el volumen del micrófono)", "Después de filtros: …", "Voz detectada (VAD): …") y en la grilla Entrada | Salida contaban doble, y el "Etiqueta: valor" de cada slider de "Configuración avanzada" ([ui_audio_params.go](internal/ui/ui_audio_params.go)). Ahora hacen salto de línea: cuando aparece "SATURA", el texto baja una línea en vez de ensanchar. La regla quedó en AGENT.md §3.D.
- [ ] **R7. Ctrl+V de imágenes trabaja en el hilo de UI**, solo si se nota: `tryPasteImage` ([ui_chat.go](internal/ui/ui_chat.go)) corre en `TypedShortcut` (callback de Fyne, goroutine 1). Ahí `readClipboardImage` ([file_windows.go](internal/ui/file_windows.go)) hace `os.ReadFile` cuando se copió un archivo en el explorador (sin tope de tamaño) y, con una captura (`CF_DIB`), `bmp.Decode` + `png.Encode`. Con una captura grande la ventana se puede trabar unos cientos de ms, y AGENT.md §3.G lo prohíbe. Arreglo probable: en el hilo de UI solo abrir el portapapeles y copiar los bytes o la ruta (`OpenClipboard`/`CloseClipboard` tienen que correr en el mismo hilo); leer el archivo y convertir a PNG en una goroutine que termina con `stageImage` vía `fyne.Do`.

## Pruebas manuales pendientes

Las anteriores se hicieron todas el 2026-11-02 y salieron bien (resumen en "Historial").

- [ ] **C3. Ventana principal** (columnas fijas + lavada de cara), con el exe nuevo, a media pantalla (~960 px) y maximizada: pegar una invitación larga no mueve las columnas, Conectar con el botón y con Enter; Iniciar sala (nueva y guardada) → el panel izquierdo se oculta solo y ☰ lo muestra y lo oculta; Invitar (Internet / LAN-VPN), mute (insignia en el avatar propio), colgar, Salir / Parar sala; "Reiniciar audio" dentro y fuera de la sala; el punto al lado del nombre se pone verde al hablar y el nombre cambia al guardarlo en Ajustes; chat: texto, 📎, Ctrl+V de una imagen (barra arriba del mensaje, ✕ la saca), un link largo (se ve acortado, abre entero, "copiar" copia todo), buscador con su ✕ y el contador, "Ver otra sala…"; botonera: buscar, agregar, reproducir, editar, atajo de un sonido y de Detener (dice "Bind" o el atajo sin "⌨"; uno largo como Ctrl+Shift+Alt+… se corta con "…" y con el mouse medio segundo encima aparece la leyenda con el atajo completo, que se va al sacar el mouse o al hacer clic, y el clic abre el diálogo a la primera), "Bloquear sonidos ajenos", volumen; avatares: slider, ping/pérdida/jitter, aro rojo, quién habla; borrar el registro; la barra de estado on/off desde Ajustes y un estado largo (se corta con "…", también en el pie de Ajustes); Ajustes se ve igual que antes y vuelve bien a la ventana principal. Con Windows en modo claro la app se ve oscura igual.
- [ ] **C4. Ajustes a media pantalla** (~960 px): abrir Ajustes no agranda la ventana y se ven "Guardar" y la tarjeta de Salida enteros, también con "Configuración avanzada" abierta en Entrada y en Salida; con el micrófono saturando, "SATURA (bajá el volumen del micrófono)" baja de línea en vez de ensanchar; los tres botones de "Interfaz y Sistema" (carpeta de datos, logs, actualizaciones) uno debajo del otro y andando.
- [ ] **C5. Registro de actividad solo de la sesión**: al abrir la app está vacío; al crear o entrar a "Mi sala" (como host y como cliente de otra instancia) no aparece nada del 27–28/09, solo lo de ahora ("X se conectó", migración de host…), en orden y con lo último abajo; el chat viejo sigue apareciendo entero; el 🗑 lo vacía y no vuelve nada viejo al reconectar.

- [ ] **Sueltos del 2026-11-02** (`selectFile` en hilo propio, `NewLazySystemDLL`, S12), con el exe nuevo: 📎 imagen y elegir sonido en la botonera (agregar y reemplazar audio), varias veces seguidas, eligiendo y cancelando; Ctrl+V de una captura (Win+Shift+S), de una imagen copiada del navegador y de un archivo copiado en el explorador; un atajo de la botonera con otra ventana adelante (un juego) y asignar uno nuevo; cerrar maximizada → abre maximizada, cerrar en tamaño normal → abre con ese tamaño; un nombre de más de 32 caracteres en Ajustes → Guardar lo deja en 32, uno normal no cambia.

---

# Ideas a futuro

### F4. Directorio de salas en un servidor propio (no volver a pasar la invitación)

- [ ] **Idea (de Coffee, 2026-10-28).** Hoy la invitación se vuelve a pasar en cada sesión porque cambia la IP pública del host (y con S4, la clave). La idea: un servidor HTTPS propio, con DNS fijo, donde el host publica "dónde está la sala" y la app de cada amigo va a buscarlo sola. Se conecta igual P2P: el servidor solo dice la dirección y nunca pasa audio.
- **Opciones que se pensaron:**
  1. El servidor guarda IP + nombre de sala **sin la clave**; la clave queda local (se pasó una vez por WhatsApp). Problema: con S4 la clave rota en cada sesión, así que habría que pedirle la nueva al host (con una contraseña) o ponerla a mano.
  2. El servidor guarda IP + clave detrás de un PIN de 4 dígitos. **No recomendado**: 10.000 combinaciones se prueban en minutos si no hay un límite estricto, y el servidor pasaría a tener las claves de todas las salas (si lo comprometen, se cae todo).
- **Propuesta para cuando se haga** (resuelve el problema de la rotación sin pasar claves por internet):
  - Un **secreto de sala de largo plazo** (32 bytes) que viaja una sola vez en la primera invitación (WhatsApp) y cada amigo guarda localmente. Nunca va al servidor.
  - **Clave de sesión derivada**: `HKDF(secreto, sal de la sesión)`. El host genera una sal aleatoria por sesión y la publica junto a la IP. La sal es pública y no sirve sin el secreto. Así la clave rota en cada sesión **sin intercambiar nada**: todos la calculan solos. No hace falta pedirle nada al host ni ponerla a mano.
  - **ID de sala en el servidor** = derivado del secreto (por ejemplo `HMAC(secreto, "id")`). Sin el secreto no se puede preguntar por la sala, así que tampoco hace falta contraseña ni PIN: conocer el secreto *es* la contraseña.
  - El host publica `{dirección(es), sal, vence}` firmado con `HMAC(secreto, …)`. El cliente verifica la firma: un servidor comprometido no puede mandar a nadie a otra dirección (y un host falso no sabría la clave igual).
  - **Migración**: el host nuevo también conoce el secreto, así que republica su dirección y el servidor apunta al host actual.
  - En la app: la sala queda guardada ("Mis salas") y entrar es un clic. Para sacar a alguien: secreto nuevo = invitación nueva.
- **Hole punching coordinado por el mismo servidor** (para hostear detrás de CGNAT o sin UPnP, sin VPN de terceros como Radmin o Tailscale):
  - **Host con IP propia y UPnP**: igual que hoy. El servidor solo publica dónde está la sala.
  - **Host detrás de CGNAT o sin UPnP**:
    1. El host se registra en el servidor **desde el mismo socket UDP de la sala**, y el servidor le contesta con qué IP:puerto pública lo ve (lo que hace STUN, pero propio).
    2. El cliente hace lo mismo y pide la sala: obtiene la dirección del host y deja anotada la suya para el host.
    3. Los dos se mandan paquetes a la vez: cada NAT ve tráfico saliente hacia el otro, abre el paso y quedan conectados **directo**. Cada cliente hace lo suyo con el host y queda la estrella de hoy (cliente ↔ host).
  - **Mismo socket para todo**: si el registro sale por otro puerto, la dirección que vio el servidor no sirve.
  - **Mantener el camino abierto**: los NAT cierran el paso con unos 30 s sin tráfico. Los pings de 2 s de VoxMesh ya lo cubren. Mientras espera a alguien, el host repite el registro cada pocos segundos (también mantiene abierto el camino al servidor).
  - **Migración**: si el host nuevo también está detrás de CGNAT, se registra y los clientes vuelven a hacer el paso 2 con él. VoxMesh ya tiene la mitad: en la migración los clientes le mandan `RoomHello` al candidato cada 500 ms para abrir camino (AGENT.md §3.E4).
  - **Cuándo falla**: NAT simétrico de los dos lados (algunos CGNAT y datos móviles). Ahí queda el relay (abajo) o una VPN.
- **El servidor no piensa** (decisión de Coffee): la inteligencia vive en VoxMesh. El servidor es un programa mínimo (por ejemplo unas decenas de líneas de Node.js con `dgram`) que solo:
  1. contesta "te veo desde tal IP:puerto" (reflejo de dirección);
  2. guarda y devuelve blobs opacos por ID de sala, con vencimiento corto: el registro firmado del host y las "notas" de los clientes que quieren entrar;
  3. aplica un límite de pedidos por IP.
  - No verifica firmas, no conoce el secreto, no decide cuándo ni cómo conectar, no reintenta nada. VoxMesh hace todo eso: verifica la firma, decide cuándo hacer el hole punching, reintenta, sigue la migración.
  - **Todo en UDP** (también el directorio): así un solo puerto sirve para el reflejo y para los blobs, y no hacen falta HTTPS ni certificados. Lo que viaja ya va firmado o es opaco. Si algún día se prefiere HTTPS para el directorio, el reflejo igual tiene que ser UDP.
- **Dónde correrlo gratis** (tiene que aceptar **UDP entrante** con IP pública):
  - **Oracle Cloud "Always Free"**: VMs gratis para siempre con IP pública y UDP. La opción más sólida.
  - **Google Cloud free tier** (e2-micro): también sirve con UDP; revisar los límites de tráfico del plan gratis.
  - **Tu propia PC o una Raspberry Pi en tu casa**: tu conexión tiene IP pública. Gratis, pero si se corta tu internet, se cae el directorio (la sala ya conectada sigue andando).
  - **No sirven** Cloudflare Workers, Vercel ni Netlify: son solo HTTP y no reciben UDP, así que no pueden hacer el reflejo de dirección. Un STUN público (Google) lo resolvería, pero es un tercero.
  - Dominio: uno propio, o un subdominio gratis (DuckDNS u otro DNS dinámico) para que la dirección del servidor no cambie.
- **Relay (muy opcional, solo si el hole punching falla seguido)**: el mismo servidor reenvía los paquetes de la sala cuando no hay camino directo. Con S4 van cifrados y el relay no lee nada. Pero pasa el audio de verdad (tráfico y latencia) y es justo la parte "con inteligencia" que se quiere evitar. No hacerlo hasta que haga falta; mientras tanto, para ese caso queda una VPN.
- **Seguridad del conjunto**: el servidor solo ve IPs y blobs. No puede entrar a la sala (no tiene el secreto) ni redirigir a nadie (los clientes verifican la firma del host). Si lo apagan o lo hackean, lo peor que pasa es que hay que volver a pasar la invitación por WhatsApp como hoy.
- **Relación con S4 + F3**: compatible. El código de invitación de F3 ya lleva versión, así que más adelante puede pasar a llevar el secreto de largo plazo en vez de la clave de sesión. No hay que cambiar nada de S4 + F3 ahora.

---

# Descartado / quizás

No suman al flujo "crear sala → pasar invitación → hablar y jugar", o S4 ya los cubre entre amigos. Se dejan anotados con el porqué para no volver a discutirlos; si aparece el problema real, se retoman.

- **S2 completo** (filtro de paquetes de direcciones desconocidas, `AddPeer` solo al aceptar `RoomHello`, no aceptar `SystemMessage` de clientes, `sync_request` solo del host, límite de `RoomHello`): con S4 un desconocido no puede mandar nada válido. Retomarlo solo si S4 se demora mucho.
- **S3. Identidad atada a la dirección** (audio, chat, `RoomLeave` y `Failover.StateFrom` creen el ID del payload): con S4 solo quedan los amigos, y entre amigos se acepta. Si algún día se hace, **ojo con LAN/VPN**: hay participantes guardados con IP privada que responden desde otra dirección (AGENT.md §3.E4 "Limits"), y un chequeo estricto en `StateFrom` rompería la migración justo en el caso VPN. Probarlo con LAN real y con VPN.
- **S6. Topes del `assets.Assembler`** (transferencias pendientes sin tope, hasta 8 MiB cada una): con S4 solo los amigos llegan ahí.
- **S9. Micrófono del celular** (`subtle.ConstantTimeCompare` en `handlePage`/`handleWebSocket`, `Origin` sin chequear): LAN y con token. Si se toca esa zona, el compare es una línea.
- **F2. Push-to-talk**: solo si lo piden los pibes. El diseño está pensado: `MicMode` + `PushToTalkKey` en `LiveSettings`, el poller de [hotkey_windows.go](internal/ui/hotkey_windows.go) reporta la tecla mantenida (`a.pttHeld atomic.Bool`, ignorada mientras se escribe en la app), en `sendOutgoingPCM` "tecla suelta" = mute (la cadena sigue, el frame no sale; el mute tiene prioridad), radio de vuelta en Ajustes, medidor amarillo "no se envía (mantené <tecla>)". Test: `shouldSend(muted, mode, held, releasedAt, now)` como función pura.
- **S7 con firma ed25519**: alcanza con HTTPS + hash.
- **F3 completo** (esquema `voxmesh://`, registro en `HKCU`, instancia única, QR): WhatsApp no hace clickeable un esquema propio, y la instancia única es lo más propenso a fallar.
- **Rendezvous / STUN / TURN** (CGNAT o NAT simétrico de los dos lados): hoy la app avisa. Alternativa sin servidor: la opción LAN/VPN de F3 (Tailscale/ZeroTier). El plan propio está en **F4** (directorio + hole punching con un servidor mínimo; relay muy opcional).
- **Mover `voxmesh.json` a `%AppData%\VoxMesh\`**: aporta poco. Si se hace, sin migración (Visión): la ruta nueva y listo; el updater no toca el json.
- **Flight recorder en el watchdog** (`runtime/trace.FlightRecorder`): solo si vuelven los cuelgues y el log no alcanza.
- **Audio: compensar el déficit del emisor** (menos de 50 frames/s, típico del Bluetooth), **time-stretching** y **P2P directo entre clientes**: el audio quedó muy bien. Retomar solo si los logs muestran cortes ("Cortes de audio" en §0). Mientras tanto, sugerirle al que usa Bluetooth el micrófono de la PC o del celular.
- **Herramientas**: `golangci-lint`, `govulncheck`, tests de cero asignaciones extra, `testing/synctest`, PGO, perfiles bajo demanda, `go fix`, `log/slog`, manifiesto DPI / `longPathAware`, firmar el binario (SmartScreen).
- **Avisar al desconectarse un dispositivo en medio de la sala** (callback `Stop` de malgo): hoy alcanza con "Reiniciar audio".

---

# Historial (hecho)

- **Versión 1.0.0** (2026-11-02): el historial de git anterior se juntó en un solo commit y el repo de GitHub se recreó (se fueron los releases 0.x, que traían el ícono viejo). Los "Hecho" de esta agenda ya no citan hashes.
- **C1. Nombres repetidos: " (2)" en vez de `_PUTO`** (2026-11-02): `room.State.UniqueUsername(id, nombre)` ([state.go](internal/room/state.go)), pura y con test (`TestUniqueUsername`): `Pepe` → `Pepe (2)` → `Pepe (3)`, sin distinguir mayúsculas y salteando los sufijos ocupados. La usa `handleRoomHello` después de `ClampUsername`. AGENT.md §3.E.

- **Ícono nuevo** (2026-11-02), para la 1.0.0: el anterior era ofensivo. Ahora es genérico: un cuadrado redondeado azul (el de los botones resaltados) con un micrófono blanco. Fuente `icon.svg`; `icon.png`, `internal/ui/icon.png`, `icon.ico` y el `.syso` regenerados (ver AGENT.md §2).

- **C5. El registro de actividad mostraba avisos de septiembre** (2026-11-02). Desde el 29/09 los avisos de la sala ("X se conectó", "X será el nuevo host"…) solo se muestran, no se guardan; pero `initHistory` seguía cargando al registro los que quedaron en el historial (en "Mi sala", el último del 28/09) y los ponía **después** de los de la sesión: al bajar solo, el registro mostraba el 28/09 y lo de hoy quedaba arriba o lo cortaba el tope de 200. Además el host los mandaba en cada sincronización de historial y también iban al final.
  - **Hecho:** el registro es solo de la sesión. `isSystemMessage` ([ui_chat.go](internal/ui/ui_chat.go)): `initHistory` ya no carga avisos, `addChatMessage` ignora los que llegan por la sincronización y `respondHistorySync` no los manda (`chatMessagesOnly`, [network_sync.go](internal/ui/network_sync.go)). Los viejos quedan en el archivo y se ignoran (sin compatibilidad). Test `TestActivityLogIsSessionOnly` ([ui_chat_test.go](internal/ui/ui_chat_test.go)). **Quedó afuera** (descartado, opción B): guardar el registro por sala para verlo en la sesión siguiente.

- **C3. Ventana principal: columnas que se estiraban y lavada de cara** (2026-11-02). Solo UI: no se tocó audio, red, sala ni config.
  - **Hecho:** columnas: la invitación pegada ensanchaba la columna izquierda y la botonera quedaba fuera de la ventana, porque `newScrollableEntry`/`newChatInputEntry` usaban `TextWrapOff` + `ScrollNone` (de fines de septiembre, sin explicación; antes eran `NewEntry` comunes). Ahora el texto se desplaza dentro de la entrada; laterales de ancho fijo (`fixedWidthLayout`: conexión 240 px, botonera 280 px) y el centro con el resto; `a.status`, `a.stagedLabel` y `a.settingsStatus` (pie de Ajustes) en el centro de su Border con "…". Test `TestMainViewIgnoresLongText` ([ui_layout_test.go](internal/ui/ui_layout_test.go)). Lavada de cara: tema propio [ui_theme.go](internal/ui/ui_theme.go) (oscuro siempre: con Windows en modo claro el texto quedaba negro sobre las tarjetas; texto deshabilitado legible, verde menos chillón), `uiCard` con el color del tema y títulos iguales (`cardTitle`/`cardHeader`). Barra de arriba: ☰ · VoxMesh · indicador de voz propia y nombre (`voiceIndicator`/`selfNameLabel`, que existían pero nunca se creaban) | Invitar · micrófono · colgar (antes a la izquierda) | **Reiniciar audio** (antes "Refresh Audio", con el easter egg) · Ajustes. Íconos del tema en vez de emojis (📎 📄 ⏹ ➕ 🔍 📷 y los 🟢🟡🟠🔴 del ping, ahora "●" de color); "Iniciar sala", "Conectar" y "Enviar" resaltados; el ✕ del buscador del chat como el de la botonera; la imagen pegada arriba del mensaje, a todo el ancho; volumen de la botonera en una fila; avatares de 30 px; hora del registro en gris; links del chat de hasta 40 caracteres a la vista (Fyne no los parte al hacer el salto de línea y se salían de la fila). Atajos de la botonera sin "⌨": "Bind" o el atajo recortado con "…" para que entre en el botón (`bindButton`, `fitButtonText`) y, con el mouse medio segundo encima, una leyenda con el atajo completo (`tooltip`: capa propia arriba de la ventana, no un PopUp de Fyne, que se come el primer clic). **Quedó afuera:** los emojis de los títulos de Ajustes y los mensajes de estado con "tú".
- **Pruebas manuales** (2026-11-02, todas bien):
  - Cancelación de eco (F1): tele/parlantes con y sin la opción, auriculares, micrófono del celular cerca de los parlantes, los dos hablando a la vez.
  - Smoke test de S1/S10/S11: imagen con 📎 y con Ctrl+V; agregar, reproducir, renombrar y borrar sonidos; historial y sonidos de "Mi sala"; la otra instancia recibe todo.
  - Actualización estando en sala (S7 + R3): la otra instancia ve la salida enseguida, el router no queda con la regla, el log viejo termina en `=== cierre` y la versión nueva arranca sola.
  - Migración de host (a)–(e), en LAN con 3 instancias y por internet.
  - Invitación y clave (S4 + F3): entrar con la invitación; una de otra sala o editada → "Código incorrecto o host inalcanzable" y "Paquetes descartados" en el host; migración sin invitación nueva; volver con la invitación que copia otro cliente; "Invitar" → LAN/VPN.
  - Lista de participantes (S2): reconectar desde otro puerto no suma participantes; el registro viejo se desconecta a los 20 s y sale de la lista a los 10 min.
  - Dispositivos (Bluetooth → HDMI + auriculares), voz fuerte con Default, pasar a Default en Entrada y Salida, y revisión en pantalla del audio configurable (acordeón, perfiles, medidores, "SATURA", aro rojo).
- **S1. Rutas de archivos desde la red** (2026-10-28):
  - [history.go](internal/history/history.go): ID `[0-9a-f]{32}` + extensión en minúsculas y en lista blanca (`.png .jpg .jpeg .gif .webp` / `.pcm .wav`), E/S dentro de `os.OpenRoot`, y `ImagePath`/`SoundPath` devuelven error.
  - `safeName` sin `<>:"|?*`, sin caracteres de control ni puntos finales, y con `_` en los nombres reservados.
  - UI: `netAssetOK`/`netSoundsOK` descartan temprano lo inválido; los envíos locales rechazan formatos fuera de la lista. Detalle en AGENT.md §K.
- **S10. Lápidas de sonidos** (2026-10-28): `history.ValidID` en `AddSoundTombstones` y al leer el archivo; `netSoundIDsOK` en los dos syncs de la botonera.
- **S11. Nombre de sala acotado** (2026-10-28): `history.ClampRoomName` (64 runas) al crear sala, en cada `RoomState` recibido y dentro de `safeName`.
- **Audio configurable** (perfiles, tabla `Params`, modo grito, medidor, defaults del perfil "Personalizado" de Coffee del 26/10), "Grabar prueba" con la cadena real, `Effects.Reset` al entrar a la sala, recorte de volúmenes al bajar el máximo por usuario, finales de línea LF (`.gitattributes`). Ver AGENT.md §3.A/§3.I.
- **Cortes de audio**: colchón por frase, PLC de 60 ms, 25 buffers de captura sin asignaciones, envío en hilo MMCSS "Pro Audio", celular con horario fijo, archivos con ritmo (~2 Mbps, la voz no pasa por esa cola), UPnP con lease de 8 h y borrado al cerrar. Simulación: captura que pierde frames 25.9 → 0.5 cortes/min.
- **Desconexiones y migración**: corte < 20 s no migra ("Reconectando…"), mismo host nuevo para todos con epoch, el host que vuelve se suma solo, `Leave` ×3, cambio de rol sin reabrir el audio. Ver AGENT.md §3.E4.
- **Dispositivos por ID de Windows** (25/10): si no está, se usa el predeterminado; Refresh Audio también fuera de la sala; log nivel 1 por defecto; error de audio al log.
- **Memoria**: `debug.SetMemoryLimit(512 MiB)` en vez de `FreeOSMemory()` cada 30 s.
- **Docs**: README reescrito y AGENT.md al día (2026-10-26).
