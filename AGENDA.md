Pendientes de Desarrollo - VoxMesh

> **Estado 2026-10-21:** todas las tareas de código de esta agenda están hechas (tests verdes con CGO, `-race` en audio/transport/phonemic/ui, build estático sin DLLs extra). Lo único que falta es lo que solo se puede probar con los dos en una sala real: ver **"Verificación en sala real"** al final. Recordar que el audio pasó a Opus **sin retrocompatibilidad**: los dos tienen que actualizar juntos.

1. Control de Audio Individual (Frontend y Backend)
[x] ~~Implementar applyPerUserGain~~ → no hizo falta (ver nota): la ganancia la aplica el mixer del Engine al sumar cada stream.
[x] Mapa seguro en App: `peerVolumes` + `peerVolumeMu` (sync.RWMutex), por ID de participante; además se guarda por nombre en `config.PeerVolumes` (el ID cambia en cada conexión). El Engine guarda su propio mapa `volumes`, así el volumen sobrevive a la purga del stream y se aplica al crearlo; `startAudio` lo reaplica al reabrir el Engine.
[x] ~~Interceptar en receiveLoop~~ → reemplazado por `Engine.SetStreamVolume(senderID, v)` (ver nota).
[x] Slider 0–300 % (0.0–3.0, paso 0.05) debajo de cada avatar de los demás, con el porcentaje al lado. Cambia el volumen al instante mientras se arrastra y lo persiste al soltar.

> Nota: el Engine ya tiene volumen por emisor: [`SetStreamVolume`](internal/audio/engine.go#L543) y se aplica al sumar en el mixer ([engine.go:360](internal/audio/engine.go#L360)). No hace falta tocar `receiveLoop` ni un `applyPerUserGain` aparte: alcanza con el mapa de volúmenes + slider que llame a `SetStreamVolume(senderID, v)` (y reaplicarlo cuando se crea el stream o se reinicia el engine).

---

# 2. Optimización del pipeline de audio

Revisión del 2026-10-21. Objetivo: menos latencia percibida y audio más prolijo ante pérdidas/lag, **sin romper lo que anda**.

**Contexto:** hoy somos dos usuarios, así que **no hace falta compatibilidad con versiones viejas**: se puede cambiar el formato de paquete, borrar el camino legacy y meter Opus directo, siempre que los dos actualicemos juntos.

**Reglas para cada tarea**
- Build/tests con CGO (si no, `internal/ui` y `engine.go` se saltean en silencio): ver §4 de [AGENT.md](AGENT.md). `go test ./...` y `go test -race ./internal/audio/`.
- Cada fase tiene que poder probarse sola en una sala real antes de pasar a la siguiente.
- Al terminar cada fase, actualizar [AGENT.md](AGENT.md) (§3.A, §3.B, §3.C, §3.H) para que el mapa siga siendo cierto.
- Los números de línea son del 2026-10-21; pueden haberse corrido.

## Presupuesto de latencia actual (boca → oído, A → host → B)

| Tramo | Aprox. | ¿Se puede bajar? |
|---|---|---|
| Captura + armado de frames de 20 ms ([engine.go:188](internal/audio/engine.go#L188)) | 10–20 ms | No |
| RNNoise (retardo estructural) | ~20 ms | No |
| Red A→host→B | variable | Indirecto: fragmentación y ancho de banda (Fase 2) |
| **Jitter buffer** (objetivo fijo 120 ms al arrancar cada frase, hasta ~160 ms) | **120–160 ms** | **Sí, Fase 4** |
| Buffer de salida WASAPI | 10–30 ms | No |
| Celular: buffer del server (100 ms) **+** jitter de sala (120 ms) | +100 ms | Sí, Fase 3 |

## Hallazgos (referencia para las tareas)

- **H1 – Limitado 5 veces.** Envío: limitador tanh *antes* de RNNoise ([effects.go:228](internal/audio/effects.go#L228)) + `ApplyPeakCeiling` 27000 ([rnnoise.go:182](internal/audio/rnnoise.go#L182)). Recepción: tanh por emisor ([network_audio.go:129](internal/ui/network_audio.go#L129)) + `busLimiter` + `softClip` ([engine.go:383-390](internal/audio/engine.go#L383-L390)). El tanh de recepción con defaults casi nunca actúa (lo que llega ya está topeado en 27000 < −1 dB ≈ 29200).
- **H2 – Conversiones PCM duplicadas.** Recepción: bytes→int16→bytes en `outputEffects.Apply` ([effects.go:130](internal/audio/effects.go#L130)) y otra vez →int16 en `jitter.Push` ([jitter.go:132](internal/audio/jitter.go#L132)). Envío: ~5 asignaciones/copias por frame (Decode, salida de RNNoise [rnnoise.go:63](internal/audio/rnnoise.go#L63), Encode, `audioPayload` [network_audio.go:87](internal/ui/network_audio.go#L87), `encode()` [udp.go:74](internal/transport/udp.go#L74)).
- **H3 – `Log10` por muestra** en `applyDynamicsFast` ([effects.go:219](internal/audio/effects.go#L219)) aunque compresor y expansor estén apagados: 960/frame al enviar + 960/frame por emisor al recibir.
- **H4 – Celular: dos buffers en serie y sin corrección de deriva.** `forwardPCM` ([server.go:304](internal/phonemic/server.go#L304)) prebufferea 100 ms y después el jitter de cada receptor suma 120 ms. Si el reloj del celular corre más rápido, la cola ([server.go:95](internal/phonemic/server.go#L95), cap = prebuffer + 12) se llena despacio hasta ~340 ms y se queda ahí.
- **H5 – Código muerto:** `limitFrame` ([engine.go:101](internal/audio/engine.go#L101)), `applyDynamics` ([effects.go:206](internal/audio/effects.go#L206)), `ProcessFrame` / `ProcessOutgoingFrame*` ([pcm.go:40-65](internal/audio/pcm.go#L40-L65)), `ProcessPCM*` ([rnnoise.go:32-52](internal/audio/rnnoise.go#L32-L52)). `jitterBuffer.Stats()` ([jitter.go:162](internal/audio/jitter.go#L162)) nunca se lee fuera de los tests: hoy no hay datos de pérdidas.
- **H6 – Fragmentación IP.** Paquete de audio = 16 header + 1 + 16 (ID hex) + 1920 PCM = **1953 bytes** > ~1472 que entran sin fragmentar. Cada frame viaja en 2 fragmentos: si se pierde uno, se pierde el frame. Algunos routers/CGNAT los manejan mal.
- **H7 – Ancho de banda.** PCM crudo ≈ 790 kbps por emisor; el host lo multiplica por cada peer. En subida hogareña llena la cola del módem (bufferbloat) → picos de lag que el jitter buffer tiene que absorber.
- **H8 – Se come el arranque de la primera sílaba.** El VAD tarda 1–2 frames en abrir y esos frames no se mandan nunca ([network_audio.go:79](internal/ui/network_audio.go#L79)).
- **H9 – Indicador "hablando" adelantado.** Se prende al *llegar* el paquete ([network_audio.go:57](internal/ui/network_audio.go#L57)), ~120 ms antes de que el audio *suene*: hace más notorio el lag.
- **H10 – PLC y deriva toscos.** PLC repite los 20 ms enteros con ×0.5 ([jitter.go:249](internal/audio/jitter.go#L249)): suena robótico. La deriva tira el frame que toque aunque caiga en medio de una vocal ([jitter.go:220](internal/audio/jitter.go#L220)).
- **H11 – `ApplyPeakCeiling` al enviar** es ganancia estática por frame ([effects.go:108](internal/audio/effects.go#L108)): escalones de volumen al gritar.
- **H12 – El host procesa antes de reenviar** ([network.go:152-154](internal/ui/network.go#L152-L154)).
- **H13 – Basura por paquete en `receiveLoop`.** `UDP.Receive` reserva 64 KB por paquete ([udp.go:216](internal/transport/udp.go#L216)); no hay `SetReadBuffer`/`SetWriteBuffer` en [`Listen`](internal/transport/udp.go#L107); el loop del host llama `addr.String()` una vez por participante en cada paquete ([network.go:30-31](internal/ui/network.go#L30-L31)) y `Tracef` evalúa `addr.String()` aunque el trace esté apagado ([network.go:27](internal/ui/network.go#L27)).
- **H14 – Camino legacy innecesario.** `isAudioSequenceValid` + `peerAudioSeq` ([network_audio.go:14-36](internal/ui/network_audio.go#L14-L36), [network_audio.go:64-70](internal/ui/network_audio.go#L64-L70)) y la rama FIFO de red en `PlayStream` ([engine.go:446-451](internal/audio/engine.go#L446-L451)). Solo existen para emisores < 0.9.7.

Ya verificado OK (no tocar): el preview del micrófono y la grabación de prueba están bloqueados en sala ([ui_settings.go:308](internal/ui/ui_settings.go#L308), [ui_settings.go:355](internal/ui/ui_settings.go#L355)), así que no pisan el estado de RNNoise; el guardado de assets ya corre en goroutine ([network_sync.go:63](internal/ui/network_sync.go#L63)).

---

## Fase 0 – Medir primero (no cambia el sonido)

- [x] Loguear cada ~10 s, en nivel 1 (control), por emisor: `Stats()` del jitter (Played / Concealed / Late / Dropped / Resets) + nivel del buffer + jitter de llegada medido (RFC 3550: `J += (|D| − J)/16`, con `D` = diferencia entre llegada real y esperada `seq*20ms`). Exponer desde `Engine` algo tipo `JitterReport() map[string]JitterStats` (copia bajo `playbackMu`) y llamarlo desde una goroutine de la UI.
  - Hecho: `Engine.JitterReport()` + `audioMonitorLoop` ([audio_monitor.go](internal/ui/audio_monitor.go)). Línea `Audio de <nombre>: reproducidos / recuperados / ocultados / tardíos / descartados / resets | pérdida del intervalo | buffer, objetivo, jitter RFC 3550, p95`. El jitter se mide por frase gracias al byte `Talkspurt` nuevo del header (sin él, el silencio del VAD se medía como retardo).
- [x] Opcional: mostrar pérdida % / jitter en el tooltip o chip del peer, junto al lag que ya existe ([peers.go:94](internal/ui/peers.go#L94)). → Chip: `🟢 45 ms · 2% · ±6 ms` (cada 2 s, pérdida del último intervalo).
- **Listo cuando:** en una sala real con el amigo se ven números de pérdida y jitter en el log. Guardar una línea base para comparar después de cada fase.

## Fase 1 – Limpieza: mismo audio, menos trabajo (cero cambio audible)

- [x] **Reenviar antes de procesar** en el host: `udp.Broadcast(packet, addr)` antes de `a.handleAudioPacket(...)` ([network.go:152-154](internal/ui/network.go#L152-L154)). (H12)
- [x] **`UDP.Receive` sin 64 KB por paquete:** buffer de lectura reutilizable (campo del `UDP`, solo lo usa `receiveLoop`) + `append([]byte(nil), buf[:n]...)` del tamaño justo. Hay que copiar sí o sí: el payload se retiene en handlers asíncronos (chat, assets). (H13)
- [x] **`SetReadBuffer` / `SetWriteBuffer` de 1–4 MB** en [`Listen`](internal/transport/udp.go#L107). (H13)
- [x] **`addr.String()` una sola vez por paquete** en `receiveLoop` (en una local) y no pasarlo a `Tracef` si el nivel no es trace. (H13)
- [x] **`Log10` solo si hace falta:** en `applyDynamicsFast` ([effects.go:214](internal/audio/effects.go#L214)) calcular `db` solo si compresor o expansor están prendidos. Test: salida bit a bit igual antes/después con limiter solo. (H3)
- [x] **Saltear `outputEffects` cuando es no-op:** ganancia 0, HP/LP apagados y pico del frame ≤ umbral del limiter → devolver el frame tal cual sin Decode/Encode ([network_audio.go:115](internal/ui/network_audio.go#L115)). Mismo resultado exacto. (H1, H2) → `Effects.ApplyOutput`. Detalle: es incluso más exacto que antes, la cadena "no-op" podía mover 1 LSB por el redondeo a float.
- [x] **Borrar el camino legacy:** `isAudioSequenceValid`, `peerAudioSeq`/`peerAudioTime`/`audioSeqMu` ([ui_main.go:86-87](internal/ui/ui_main.go#L86-L87)), la rama sin `FlagAudioSequence` en `handleAudioPacket`, y en `PlayStream` la conversión jitter→FIFO ([engine.go:446-451](internal/audio/engine.go#L446-L451)). `PlayStream` queda solo para `__sfx__` / `__local__`. (H14) → Hecho; el audio sin `FlagAudioSequence` ahora se ignora.
- [x] **Borrar código muerto** (H5) y los tests que solo cubren eso. → Fuera `limitFrame`, `applyDynamics`, `ProcessFrame`/`ProcessOutgoingFrame*`/`Source`, `ProcessPCM*`, `ApplyPeakCeiling`, `SmoothTransition`, `Effects.Apply` y sus tests. `Stats()` ahora se usa (Fase 0).
- **Listo cuando:** tests verdes con CGO y `-race`, sala real suena igual que antes.

## Fase 2 – Opus (el cambio grande: arregla H6, H7 y reemplaza el PLC casero)

Sin retrocompatibilidad: el audio de red pasa a ser **siempre Opus**, mismo `PacketAudio`.

**Dependencia y build**
- [x] Librería: `gopkg.in/hraban/opus.v2` (cgo sobre libopus) con build tag `nolibopusfile` (no necesitamos decodificar archivos `.opus`, así no hace falta opusfile).
- [x] Local: `pacman -S mingw-w64-ucrt-x86_64-opus mingw-w64-ucrt-x86_64-pkgconf` en msys2. Agregar `-tags nolibopusfile` en [build.ps1](build.ps1) y en el comando manual de AGENT.md §4.
- [x] CI: sumar esos paquetes al `install:` de `setup-msys2` en [release.yml](.github/workflows/release.yml#L34) y el tag al `go build` ([release.yml:52](.github/workflows/release.yml#L52)).
- [x] Verificar que el `-extldflags -static` linkea `libopus.a` (el `.exe` tiene que seguir sin DLLs extra: `ldd dist/voxmesh.exe` o Dependencies). → Verificado con `objdump -p`: solo DLLs del sistema (GDI32, KERNEL32, api-ms-win-crt-*, OPENGL32, SHELL32, USER32).

**Envío** (un encoder por App, se recrea al conectar)
- [x] Encoder 48 kHz mono, `AppVoIP`, bitrate ~32–48 kbps, complexity 8–10, **in-band FEC on**, `PacketLossPerc` 5–10 (o lo que dé la Fase 0), DTX de Opus **off** (el VAD/compuerta propio ya hace DTX). → 40 kbps, complexity 9, FEC on, 8 % (constantes en [config/audio.go](internal/config/audio.go); ajustar `OpusPacketLossPerc` con lo que mida la Fase 0).
- [x] Encodear una sola vez en `sendOutgoingPCM` ([network_audio.go:73](internal/ui/network_audio.go#L73)), después de toda la cadena de filtros; buffer de salida reutilizable. `LiveMonitoring` sigue usando el PCM sin encodear.
- [x] Achicar el ID del emisor en el paquete (opcional): hoy 16 chars hex; con Opus pesa poco, pero 8 bytes crudos o un índice de sala de 1 byte lo deja en ~100 bytes por paquete. → 8 bytes crudos (el receptor lo vuelve a hex). Paquete total ≈ 16 + 1 + 8 + ~110 bytes.
- El camino del celular no cambia: el celular sigue mandando PCM por LAN al server ([server.go:215](internal/phonemic/server.go#L215)) y la PC lo encodea en `sendOutgoingPCM` como cualquier frame.

**Recepción** (decoder por emisor, decodificado al reproducir)
- [x] Los slots del jitter buffer guardan los **bytes Opus** (no PCM) y se decodifica en `nextFrame` ([jitter.go:201](internal/audio/jitter.go#L201)), en orden:
  - frame presente → `Decode`;
  - falta N y está N+1 → `DecodeFEC(N+1)` para recuperar N;
  - falta sin FEC → `Decode(nil)` = PLC nativo de Opus (reemplaza `fillFromLast` / `JitterPLCAttenuation`).
- [x] El decoder entra al `jitterBuffer` como interfaz (`Decode/DecodeFEC/PLC`) para que [jitter_test.go](internal/audio/jitter_test.go) pueda usar un fake sin CGO.
- [x] Buffers de decodificación preasignados: corre dentro del callback de malgo bajo `playbackMu` (regla §3.G: nada de allocations). Con 1–2 emisores el costo es despreciable (~decenas de µs por frame).
- [x] Revisar la lógica de empalme ([jitter.go:169](internal/audio/jitter.go#L169)): los frames PLC/FEC de Opus salen continuos con el anterior, así que pueden contar como contiguos (sin crossfade). Mantener el fade a silencio cuando el buffer queda vacío. → Real/FEC/PLC cuentan como contiguos; el fade ahora es un frame de PLC de Opus con rampa a 0 en 10 ms (continúa la voz en vez de repetir el último frame).
- [x] Sacar el chequeo `len(pcm) < 960` de `handleAudioPacket` ([network_audio.go:50](internal/ui/network_audio.go#L50)) y reemplazarlo por un mínimo razonable para Opus.
- [x] `outputEffects` pasa a aplicarse al PCM decodificado. Ideal: moverlo al Engine **por stream** (después del jitter, en orden de secuencia), que de paso arregla la deuda de §3.H "outputEffects compartido entre emisores". → Hecho: `stream.fx` por emisor dentro del Engine, configuración vía `Engine.SetOutputFilter`.
- **Listo cuando:** paquetes < 200 bytes (Wireshark o log), sala real sin cortes, y en la Fase 0 la pérdida no empeora (debería mejorar por H6).

## Fase 3 – Que llegue prolijo

- [x] **Pre-roll del VAD** (H8): guardar los últimos 1–2 frames ya procesados (RNNoise corre siempre aunque la compuerta esté cerrada). Cuando la compuerta pasa de cerrada a abierta, mandarlos primero con secuencia contigua. El fade-in de `SmoothTransition` ([effects.go:86](internal/audio/effects.go#L86)) tiene que aplicarse al primer frame del pre-roll, no al actual. No suma latencia: el receptor igual espera el colchón al arrancar la frase. → 2 frames (`AudioPreRollFrames`). El emisor marca la frase nueva en el byte `Talkspurt` del header.
- [x] **Un solo limitador con envolvente al final del envío** (H1, H11): reemplazar `ApplyPeakCeiling` ([rnnoise.go:135](internal/audio/rnnoise.go#L135), [:159](internal/audio/rnnoise.go#L159), [:182](internal/audio/rnnoise.go#L182)) por un limitador con attack/release (misma idea que [`busLimiter`](internal/audio/limiter.go), estado dentro de `Effects`). Evaluar sacar el tanh de antes de RNNoise (distorsiona lo que entra al denoiser) o dejarlo solo como protección si el usuario sube `InputGainDB`. → Decidido: el tanh de entrada solo actúa con `InputGainDB > 0`. El limitador final (attack instantáneo, release 60 ms) corre sobre todos los frames; su techo es 27000 o el umbral del limiter del usuario si es más bajo.
- [x] **Deriva inteligente** (H10): en [jitter.go:220](internal/audio/jitter.go#L220), cuando hay que descartar, postergar hasta encontrar un frame de baja energía (RMS bajo umbral, constante nueva en [config/audio.go](internal/config/audio.go)), con tope (si el span supera objetivo + N, descartar igual). Con Opus, el "frame" a medir es el decodificado: decidir el descarte después de decodificar o usar el tamaño del paquete como proxy de energía. → Se decide después de decodificar (el decoder no pierde continuidad). Tope: `JitterDriftForceFrames` = 4. Un frame *perdido* con el buffer sobrado se saltea en vez de ocultarse.
- [x] **Celular: corrección de deriva** (H4): en `forwardPCM` ([server.go:304](internal/phonemic/server.go#L304)), si `len(s.frames) > prebufferFrames + 2`, descartar un frame (idealmente uno silencioso). → `shouldDropForDrift`: silencioso con > prebuffer + 2; cualquiera con > prebuffer + 6.
- [x] **Celular: bajar el buffer por defecto** de 100 a ~40 ms (`PhoneMicBufferMS` en [config.go](internal/config/config.go)): el jitter buffer de la sala ya absorbe el jitter del Wi-Fi. Probar también con buffer desactivado. → Default 40 ms. Ojo: un `voxmesh.json` existente conserva el valor guardado (100); cambiarlo a mano en Ajustes para probar.
- **Listo cuando:** el arranque de las frases ya no se escucha cortado, los gritos no "bombean" y una sesión larga con el celular no acumula retardo.

## Fase 4 – Menos lag percibido

- [x] **Jitter buffer adaptativo** (la mejora más grande en lag): con el jitter medido en la Fase 0, `targetFrames = clamp(ceil(p95_jitter / 20ms) + 1, 2, JitterBufferMS/20)`. El slider de ajustes pasa a ser el **máximo** (renombrar el label, AGENT.md §3.I). Cambiar el objetivo **solo al arrancar una frase** (`!j.playing`, [jitter.go:205](internal/audio/jitter.go#L205)) para que no haya saltos audibles. Con red buena puede bajar de 120 a 40–60 ms: 60–80 ms menos de punta a punta. Constantes nuevas (ventana del p95, margen) en [config/audio.go](internal/config/audio.go). → Ventana 500 llegadas (~10 s), histograma de 5 ms, margen 1 frame, mínimo 50 muestras (antes de eso usa el máximo). Label: "Buffer de recepción máximo (adaptativo)".
- [x] **Indicador "hablando" sincronizado con lo que suena** (H9): en vez de marcar en `handleAudioPacket` ([network_audio.go:57](internal/ui/network_audio.go#L57)), que el Engine registre por stream cuándo `jitter.Read` devolvió audio (timestamp atómico o campo leído bajo `playbackMu`) y que el barrido de speaking ([peers.go:133](internal/ui/peers.go#L133)) lo consulte. Sin `fyne.Do` desde el callback de audio. → `Engine.AudibleStreams` (marca `lastAudible` en el callback) consultado cada 100 ms por `audioMonitorLoop`.
- **Listo cuando:** la latencia boca → oído medida baja (prueba simple: aplauso grabado en las dos puntas, o comparar con el log de jitter) y el avatar se prende al mismo tiempo que se escucha.

## Descartado / reemplazado

- ~~PLC por período de pitch (estilo G.711 Apéndice I) en `fillFromLast`~~ → lo reemplaza el PLC + FEC nativo de Opus (Fase 2). Retomarlo solo si se abandona Opus.
- ~~Negociación de codec / `Kind` nuevo / respaldo PCM~~ → innecesario sin retrocompatibilidad.

## Verificación en sala real (no es código: la tienen que hacer ustedes dos)

**Simulación local (hecha):** [loopback_test.go](internal/audio/loopback_test.go) corre una sala en tiempo real por UDP de verdad en 127.0.0.1 (encoder Opus, header con talkspurt, receptor, jitter buffer y mezclador a ritmo de 20 ms), 3 frases con silencios, 3 % de pérdida y 0–25 ms de jitter. Resultado (estable en 3 corridas y con -race): paquete máx **152 bytes**; 12 perdidos en la red → **11 recuperados con FEC**, 1 ocultado con PLC, 0 tardíos, 0 descartados; jitter medido ~10 ms, p95 25 ms; **colchón adaptado a 60 ms** (máximo 200); **0 buffers de silencio dentro de las frases**. Es la línea base simulada; no reemplaza la prueba con oídos y una red real.

Todo lo de arriba está implementado y testeado con unit tests; estos son los "Listo cuando" de cada fase, que solo se pueden comprobar con los dos conectados y la misma versión instalada:

- Fase 0: con log nivel 1, juntar unas líneas `Audio de ...` como línea base (pérdida, jitter, p95, objetivo).
- Fase 1: que suene igual que antes.
- Fase 2: paquetes < 200 bytes (Wireshark) y sin cortes; la pérdida del log no tiene que empeorar (debería mejorar por no fragmentar).
- Fase 3: el arranque de las frases no se escucha cortado, los gritos no bombean, y una sesión larga con el celular no acumula retardo (probar también con el buffer del celular desactivado).
- Fase 4: el objetivo del log baja con buena red (ej. 40–60 ms en vez de 120) y el avatar se prende cuando se escucha, no antes.
