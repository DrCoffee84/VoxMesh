# AGENDA VoxMesh

## Visión (leer primero)

VoxMesh es **un Discord simple para jugar con los pibes**, no una plataforma pública. El flujo que tiene que andar siempre:

> Creo la sala → copio la invitación → la paso por WhatsApp → la pegan → estamos en llamada, hablando mientras jugamos y jodiendo con la botonera.

Criterios para decidir qué se hace:
- **Seguro contra desconocidos de internet**: alguien que leyó el código y conoce o escanea la `IP:puerto` no tiene que poder ejecutar nada en la PC, tocar archivos, escuchar la sala ni meterse. No hace falta protegerse de los propios amigos.
- **Simple y que funcione**: nada de features "por las dudas". Cada cosa de más es un lugar más donde aparecen errores. Lo que no aporta al flujo de arriba va a "Descartado / quizás".
- **Sin compatibilidad hacia atrás**: una versión nueva no tiene que hablar con la anterior ni leer datos viejos (salas, historial, manifests, config, perfiles). Hoy existe una sola sala real. Se borra lo viejo sin transición, sin shims y sin migraciones; si un dato viejo no carga, se ignora. **Única excepción**: el updater de la versión instalada tiene que poder bajar e instalar la nueva.
- Principalmente por internet. LAN/VPN (Tailscale, ZeroTier, Radmin) también tiene que funcionar, porque es la forma de usarla sin abrir puertos.
- **Para usuarios comunes** (2026-11-13): la tiene que poder usar alguien que no es técnico sin que le cueste. Un amigo de Coffee es la vara: si algo le resulta "una paja", es un error de la app.

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

### Prioridad (2026-11-13)

Va antes que todo lo demás. Detalle en "Próximo: cómoda para usuarios comunes". **Un chat por fila**, en este orden. C8 + C9, C10 + C11 + C12, F6 + F7, C18 + C13 + C2 (con R6) y C14 hechos el 2026-11-13 (falta probarlos a mano); de paso apareció C17 (el registro de actividad queda oculto dentro de la sala). El 2026-11-13 se sumó C18 (gráfico de lag): va con C13 en la fila 4 y C14 pasa a su propia fila. También el 2026-11-13: F8 (que un grito no deje sordo), C19 (plantilla fija "Sin filtros") y C20 (apagar los atajos de la botonera), juntos en la fila 6, antes de C15 y C16 para que el lavado de cara cubra los controles nuevos. Decidido con Coffee el 2026-11-13: C17 queda como está (el registro se ve con ☰) y C2 entra en la fila 4. C21 + C20 hechos el 2026-11-14 (falta probarlos a mano); de paso aparecieron R10 (apagar Windows no cierra la sala), C24 (el foco de una entrada frena los atajos de una tecla) y C25 (el atajo "Numpad +"). Después se sumó C21 (la X manda la app al área de notificaciones): va con C20 en la fila 6, porque los dos sirven para tener VoxMesh de fondo mientras se juega. F8 + C19 pasan a la fila 7, y con ellos C22 (slider de "desde qué volumen me toma la voz" debajo del propio nombre), que también cambia el perfil de Entrada. F8 + C19 + C22 + C23 hechos el 2026-11-14 (falta probarlos a mano); de paso apareció T4 (`TestEchoReference_Concurrent` falla a veces con `-race`). C15 hecho el 2026-11-14 (falta probarlo a mano). El 2026-11-14 Coffee sumó la fila 9 (C26 a C29, cambios chicos) antes de C16, que pasa a la 10.

| # | IDs | Qué, en simple | Tamaño |
|---|-----|----------------|--------|
| 1 | **C8 + C9** | "Invitar" muestra todas las direcciones de entrada, dice "Abriendo puerto…" y el error del puerto va en rojo; en Ajustes, cuánto dura el puerto abierto (8 h por defecto) y el registro avisa cuándo vence | Mediano. Mismos archivos que C6 (`host_network.go`, `configureHostNetwork`, `Params`) |
| 2 | **C10 + C11 + C12** | "Acerca de" (versión, commit, autor); al abrir la app con el nombre "Usuario", pedir uno (con "No volver a preguntar"); cruz para cerrar la imagen ampliada | Chico |
| 3 | **F6 + F7** | "Detener para todos" corta el sonido en toda la sala; botón para ensordecer (no escuchar lo que llega sin salir de la llamada) | Mediano. F6 suma un mensaje de red; F7 toca el Engine |
| 4 | **C18 + C13 + C2** | Gráfico del lag debajo de cada nombre (oculto por defecto, un ícono lo muestra; toda la sesión en memoria) y el registro avisa el lag largo (desde cuándo y cuánto duró); quien se reconecta desde la misma IP sigue siendo él (no "Nombre (2)"), así su gráfico no se parte | Mediano-grande. C18 suma un mensaje del host (los ms de cada uno); C13 lee las mismas muestras; C2 va en `handleRoomHello` |
| 5 | **C14** | Los problemas que hoy solo van al log o a la barra (dispositivo, celular, micrófono que pierde audio…), en el registro y con cómo arreglarlos | Mediano |
| 6 | **C21 + C20** | VoxMesh de fondo mientras jugás: la X la manda al área de notificaciones (configurable, prendido por defecto), con un menú para colgar, mutear, ensordecer y salir; un botón (con su atajo, como "Detener") que prende y apaga los atajos de la botonera | Mediano. C21 suma el ícono de bandeja de Fyne y el control de una sola instancia; C20 es chico |
| 7 | **F8 + C19 + C22 + C23** | Que un grito no deje sordo a nadie (el que escucha baja los gritos que le llegan, con su propio slider en Salida); plantilla fija "Sin filtros" en Entrada y Salida, que no se puede pisar al guardar, para probar los filtros de a uno; debajo de mi nombre, un slider de desde qué volumen me toma la voz, con el nivel en vivo detrás; los sliders de volumen con curva logarítmica (como el oído) | Mediano-grande. F8 toca la mezcla del Engine; C19 generaliza el perfil fijo "Default"; C22 guarda en el perfil de Entrada por el mismo camino que C19; C23 es chico y toca los mismos sliders |
| 8 | **C15** | Paletas de colores en Ajustes: naranja para lo que resalta, plantillas y paletas propias | Grande. Antes que C16 |
| 9 | **C26 + C27 + C28 + C29** | Cambios chicos pedidos el 2026-11-14: el aviso de la bandeja dice "Ocultar"; clic izquierdo en el ícono de la bandeja abre la app (el derecho, el menú); el log más nuevo nunca se borra; el renglón del chat se ilumina al pasar el mouse por su ícono de copiar | Chico |
| 10 | **C16** | Lavado de cara de toda la app (botones, Ajustes) sin perder el estilo clásico de escritorio | Grande. Al final: cubre los botones nuevos de las filas anteriores |

### Prioridad anterior (2026-11-11)

C6 + C7 hechos el 2026-11-12. De R8 se hizo el código; queda el diagnóstico con los logs. Detalle en "Prioridad: invitar sin errores".

| # | ID | Qué, en simple | Tamaño |
|---|----|----------------|--------|
| 1 | **C6 + C7** | "Invitar": "Obteniendo IP pública…" en naranja mientras se abre el puerto; el registro de actividad dice en una o dos líneas si se pudo abrir y qué hacer si no; las IPs LAN/VPN ocultas salvo que se activen en Ajustes | Mediano. Mismo chat (los tres tocan el mismo estado y `hostInviteOptions`) |
| 2 | **R8** | Por qué al amigo le salió "Código incorrecto o host inalcanzable" (pedir los logs **ya**: se borran a las 48 h) y que el que entra vea qué hacer. Con C7 la causa más probable (copiar una LAN/VPN) casi deja de pasar | Chico. El diagnóstico se puede adelantar |

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
  - (2026-11-13) Coffee: con la versión actual "nunca más falló, se reinició ni nada". Si sigue así un tiempo, pasar a "Historial".
- [ ] **Crashes del amigo (cliente)**: pedirle el final del log de la sesión sin `=== cierre` y el "Error de aplicación" del Visor de eventos. Su log mostraba bloqueos de UI con la goroutine 1 `[runnable]` (la CPU ocupada). El watchdog nuevo ya registra CPUs, heap, GC y las goroutines que ocupan CPU. Desde el cambio del lease UPnP (1 h → 8 h) **no hubo más crashes**: si vuelven, ver R1. (2026-11-13) Sigue sin fallar.
- [ ] **Cortes de audio**: el audio quedó muy bien. Si alguien vuelve a notar cortes, medir con log nivel 1:
  - Quien escucha: `cortes=N (X ms)` por emisor en la línea `Audio de …` (`frases` son pausas, no cortes).
  - Quien habla: `Envío: capturados=… (50.0/s) descartados=… cola máx=…`. Menos de 50/s, descartados o cola alta = su PC o micrófono pierde audio (típico del Bluetooth).
  - Con nivel 2 queda `%AppData%\VoxMesh\llegadas-*.csv`: `VOXMESH_ARRIVALS=<csv> go test -run TestJitterReplayArrivals -v ./internal/audio/` reproduce esa red por el jitter buffer.

# Próximo: cómoda para usuarios comunes (2026-11-13)

**Contexto común.** La app anda estable (Coffee, 2026-11-13: "nunca más falló, se reinició ni nada"). Lo que sigue es que la use gente que no es técnica: un amigo de Coffee es la vara ("hay cosas que eran una paja"). Si algo le cuesta, es un error de la app. Casi todo es UI. Cambian la funcionalidad F7 (ensordecer, a propósito), F6 (un mensaje de red nuevo, `sound_stop`), C9 (la duración del puerto pasa a ser configurable), C18 (un mensaje nuevo: el host les manda a los clientes los ms de cada uno) y F8 (los gritos que llegan se bajan). El orden y los grupos están en la tabla "Prioridad (2026-11-13)".

**Regla del registro de actividad** (C8, C9, C13, C14; la misma que C6):
- Solo local: `addActivity`, nunca `addSystemMessage`.
- Una línea por evento, sin repetir: tope por tipo.
- Cada problema dice qué hacer, en simple.
- Mismas claves de i18n que la barra de estado (unificar, no duplicar), en es y en. El detalle técnico va al log.
- El registro está en la columna izquierda. Dentro de la sala esa columna se oculta y se ve tocando ☰ (C17: queda así, decidido con Coffee el 2026-11-13). La barra de estado se puede apagar.

### C8. "Invitar": todas las direcciones por defecto, "Abriendo puerto…" y el error del puerto en rojo

- [x] **Pedido (Coffee)**: que "Invitar" muestre todas las invitaciones de entrada, también las LAN/VPN. Le gusta el renglón naranja de espera, pero que diga también que está abriendo el puerto. Si el puerto no se abre, que salga en rojo en el mismo menú, algo simple tipo "Error al abrir el puerto: mirá el registro".
  - **Hecho** (2026-11-13): `show_lan_invites` con default 1 en `config.Params` (sin migración; la ayuda dice que se pueden ocultar). `inviteOptions` ([host_network.go](internal/ui/host_network.go)): mientras busca, el renglón naranja usa `conn.net_finding`, el mismo texto que la barra y el registro, ahora en el orden real ("Abriendo el puerto y obteniendo la IP pública…"); se borró `invite.finding`. Con `hostNetNoUPnP`, arriba el renglón rojo `invite.port_failed` ("Error al abrir el puerto: mirá el registro de actividad"), que no copia, y abajo "Internet · IP:puerto (puerto sin abrir)" en naranja y copiable, para quien abrió el puerto a mano. Los renglones rojos (también sin IP y CGNAT) llevan `inviteOption.showLog`: tocarlos cierra el popup y muestra la columna del registro (`showConnectionPanels`, [ui_invite.go](internal/ui/ui_invite.go)), porque al entrar a la sala esa columna se oculta (ver C17). `invite.lan` termina en "(misma red o VPN)". Con las LAN/VPN a la vista, "Invitar" siempre abre el popup (`copiesDirectly` quedó igual). Tests `TestInviteOptions` (los renglones de internet en orden, por estado), `TestInviteOptionsTexts` y `TestInviteOptionsCopyNotice`. **Quedó afuera**: el aviso al copiar una LAN/VPN sigue siendo el normal (`invite.copied`).
- **Hoy** (C6 + C7):
  - `show_lan_invites` tiene default 0 (`config.Params`).
  - `inviteOptions` ([host_network.go](internal/ui/host_network.go)): con `hostNetFinding`, "Obteniendo IP pública…" (`invite.finding`) en naranja; con `hostNetNoUPnP`, "Internet · IP:puerto (puerto sin abrir)" en naranja y copiable (`invite.internet_port_closed`; al copiar, `invite.copied_port_closed`).
  - El renglón gris `invite.lan_hint` solo aparece con el check apagado.
- **Arreglo propuesto**:
  - `show_lan_invites` con default **1**. Sin migración: quien ya guardó la config con 0 sigue en 0 (Visión). La opción queda en Ajustes, para ocultarlas.
  - `invite.finding` → "Abriendo puerto y obteniendo IP pública…" (en: "Opening the port and getting the public IP…"). Alternativa, a decidir en el plan: dos fases ("Abriendo puerto…" mientras corre `MapUDP`, "Obteniendo IP pública…" mientras corre ipify), con un estado más en `hostNetwork`. Lo simple es un solo texto.
  - `hostNetNoUPnP`: primer renglón **rojo** que no copia, "Error al abrir el puerto: mirá el registro de actividad". Para un usuario común "los logs" es el registro de actividad, que está en pantalla y ya dice qué hacer; la carpeta de logs no. Abajo, "Internet · IP:puerto" copiable, para quien abrió el puerto a mano (con `invite.copied_port_closed` al copiar). Confirmar en el plan si ese renglón va naranja o normal.
  - Cambia la decisión de C6/C7 ("mientras se obtiene la IP pública no se sugiere LAN/VPN"): Internet sigue primero, pero LAN/VPN se ve de entrada. Riesgo: vuelve la hipótesis 1 de R8, copiar una LAN/VPN sin estar en la misma red. Lo amortiguan el orden y el aviso `room.join_failed_lan` del que entra. Ver en el plan si los renglones LAN/VPN dicen "(misma red o VPN)".
  - Con las LAN/VPN a la vista nunca hay copia directa (`copiesDirectly`): siempre se abre el menú. Confirmar con Coffee que está bien.
- **Tests**: `TestInviteOptions` y `TestHostNetMsg` con el renglón rojo; la fila de `Params` con default 1.
- **Docs**: README y README.es (las LAN/VPN aparecen solas y el check sirve para ocultarlas), AGENT.md §3.E3 ("Invitar"), DESARROLLO.

### C9. Duración del puerto abierto (UPnP) en Ajustes y en el registro

- [x] **Pedido (Coffee)**: en Ajustes, cuánto tiempo queda abierto el puerto. Por defecto 8 h (él lo va a cambiar), con una descripción: al vencer se puede cortar la sala y hay que volver a iniciarla. Que también lo diga el registro de actividad, así la gente lo puede arreglar sola.
  - **Hecho** (2026-11-13): `upnp_lease_hours` (default 8, de 1 a 24 h, paso 1, "Fuera de los perfiles") en `LiveSettings` + `config.Params` + `param.upnp_lease_hours.label/.help`, con slider y ayuda en "Red y Conectividad", debajo de "Host de respaldo" ([ui_settings.go](internal/ui/ui_settings.go)). Se borró `upnpLeaseSeconds`. `configureHostNetwork` ([ui_connection.go](internal/ui/ui_connection.go)) lee la duración al abrir el puerto (al iniciar la sala y en `takeOverHost`). Cuando se invita con esa regla (lista o doble NAT), guarda en `hostNetwork` cuándo la aceptó el router y cuánto dura, y la línea del resultado suma "El puerto queda abierto hasta las HH:MM (8 h)." o "hasta mañana a las…" (`hostNetMsg` + `portLeaseMsg`, [host_network.go](internal/ui/host_network.go), `i18n.Join`). Después espera en `waitPortLease`: mira la hora del reloj una vez por minuto (el router cuenta el tiempo real, también con la PC suspendida) y, si la sala sigue y el estado es el mismo, escribe **una** línea `conn.port_expired` en la barra y en el registro; termina con el `stop` del rol de host. Tests `TestPortLeaseMsg`, `TestHostNetMsgWithLease` y `TestNetworkParamDefaults` (esta también cubre la fila de `show_lan_invites` de C8). **Quedó afuera**: renovar la regla antes de que venza (borrar + agregar en plena llamada es lo que falló en el router de Coffee; si falla el agregar, el puerto se cierra antes de tiempo); que el menú "Invitar" cambie al vencer (sigue mostrando "Internet" normal); el puerto vencido no se saca de `a.upnpMapping`, así que al cerrar la sala el log puede tener un ERROR de "borrar UPnP" sin consecuencias.
- **Hoy**: `upnpLeaseSeconds = 8 * 3600` es una constante en [ui_connection.go](internal/ui/ui_connection.go) y la regla no se renueva: el router contesta 718 si se vuelve a agregar una regla que ya existe (AGENT.md §3.E3). Con el lease viejo de 1 h, el 2026-10-26 el puerto se cerró en plena llamada. Nada avisa cuándo vence.
- **Arreglo propuesto**:
  - Param `upnp_lease_hours`: default 8, mín 1, máx 24, paso 1, unidad "h", en `LiveSettings` ("Fuera de los perfiles"). Va en la tarjeta "Red y Conectividad", con ayuda: "Cuando se cumple, el router cierra el puerto: la llamada se puede cortar y hay que volver a iniciar la sala. Se aplica la próxima vez que inicies una sala."
  - Sin "0 = para siempre": algunos routers lo toman como regla fija y, si la app se cierra mal, el puerto queda abierto. IGDv2 admite hasta 7 días; el máximo se decide en el plan.
  - `configureHostNetwork` lo lee al abrir el puerto (también al quedar de host, en `takeOverHost`).
  - Registro: la línea del resultado (`conn.net_ready`, regla de dos líneas de C6) suma "puerto abierto hasta las HH:MM". Al vencer, un timer del rol de host (`hostRole`, solo si UPnP anduvo) agrega **una** línea: "Se cumplió el tiempo del puerto (8 h): si alguien no puede entrar o se corta, volvé a iniciar la sala. En Ajustes → Red podés subir la duración."
  - **A considerar (no lo pidió)**: renovar antes de que venza, borrando y volviendo a agregar la regla (lo mismo que ya hace `addMapping` con una regla vieja de esta PC); así el vencimiento deja de importar. Decidirlo en el plan con Coffee: si se hace, cambian los textos.
- **Tests**: la fila de `Params`; una función pura (inicio + duración → texto de la línea). A mano: duración 1 h, la hora en el registro y la línea al vencer.
- **Docs**: README (qué hacer cuando vence), AGENT.md §3.E3 y §3.I.

### C10. "Acerca de"

- [x] **Pedido (Coffee)**: un botón "Acerca de" con la versión y el autor (git).
  - **Hecho** (2026-11-13): `version.Commit` + `version.CommitText()` ("dev" si está vacío, test `TestCommitText`), inyectado con `-X voxmesh/internal/version.Commit=$(git rev-parse --short HEAD)` en [build.ps1](build.ps1) y [release.yml](.github/workflows/release.yml). Diálogo en [ui_about.go](internal/ui/ui_about.go) (`showAbout` → `aboutContent`): ícono, "VoxMesh", "Versión X · commit Y" (`aboutVersionText`), "Hecho por **DrCoffee84**" con link a `https://github.com/DrCoffee84/VoxMesh`, "Licencia MIT", "Buscar actualizaciones" (deja el diálogo abierto: el resultado sale encima) y "Cerrar". Se abre desde el botón "Acerca de VoxMesh" de Ajustes → Interfaz y Sistema (debajo de "Buscar actualizaciones", uno por renglón como dice C4) y tocando la versión de la barra de estado, que pasó de `Label` a link (`widget.Hyperlink` con `OnTapped`; un botón chato salía en negrita). Textos con el prefijo nuevo `about.` (sumado a `keyLiteral`). Test `TestAboutContent` (es/en, con y sin commit, link y botón). **Quedó afuera**: el nombre real (está en LICENSE); el commit no marca si el build tenía cambios sin commitear.
- **Hoy**: la versión aparece en la barra de estado (`buildVersion`, que se puede ocultar) y sale de `version.Value`, que inyectan `build.ps1` y `release.yml` con `-X voxmesh/internal/version.Value=…`. No hay commit ni autor.
- **Arreglo propuesto**:
  - Botón "Acerca de VoxMesh" en Ajustes → "Interfaz y Sistema", al lado de "Buscar actualizaciones". Tocar la versión en la barra de estado también lo abre.
  - El diálogo muestra: ícono, versión, commit, autor con link al repo de GitHub (`DrCoffee84/VoxMesh`), licencia MIT y "Buscar actualizaciones".
  - El commit es un `version.Commit` nuevo: `-X` con `git rev-parse --short HEAD` en `build.ps1` y `release.yml`; "dev" si falta.
  - Decidir en el plan qué nombre de autor se muestra (el usuario de GitHub o el nombre).
- **Tests**: los textos en los dos idiomas (`TestCatalogParity`). **Docs**: AGENT.md §4 (`version.Commit`).

### C11. Nombre por defecto: preguntarlo al abrir la app

- [x] **Pedido (Coffee)**: si tu nombre es el de fábrica ("Usuario"), que al abrir la app salga un popup para cambiarlo, con un check "No volver a preguntar". Solo al abrir la app.
  - **Hecho** (2026-11-13): [ui_username_prompt.go](internal/ui/ui_username_prompt.go). `needsUsernamePrompt(cfg)` (pura): pregunta si el nombre está vacío o es uno de fábrica de cualquier idioma del catálogo ("Usuario", "User"), sin importar mayúsculas y también con el sufijo del host ("Usuario (2)", que un cliente guarda en su config), salvo `UsernamePromptOff`. `Run` llama a `askUsernameAtStart(a.startUpdateCheck)` después de `window.Show()`: la búsqueda automática de actualizaciones arranca recién cuando se cierra el diálogo (o enseguida si no hace falta), así su aviso nunca lo tapa. No hay otro diálogo al arrancar (ni restauración de sala). Diálogo `showUsernamePrompt`: "¿Cómo te llamás?", "Así te ven los demás en la sala. Después lo podés cambiar en Ajustes.", entrada con el foco (`escapeEntry`: Esc = "Ahora no", porque los diálogos de Fyne 2.6.3 no lo atienden; Enter guarda), "No volver a preguntar", "Ahora no" y Guardar (deshabilitado con la entrada vacía). Guardar hace lo mismo que Ajustes con el nombre (`room.ClampUsername`, `updateConfig` + `saveConfig`, `selfNameLabel`). Cerrar sin guardar no toca el nombre y vuelve a preguntar la próxima vez, salvo con el check, que guarda `username_prompt_off` (campo de `Config`, grupo "Otros"). Tests `TestNeedsUsernamePrompt`, `TestUsernamePromptSaves`, `TestUsernamePromptLater` (Ahora no / Esc, con y sin check) y `TestAskUsernameAtStartSkips`. **Quedó afuera**: el servidor del celular, si ya estaba prendido al arrancar, sigue anunciando el nombre viejo hasta que se reinicia (igual que al cambiarlo en Ajustes).
- **Hoy**: `config.DefaultUsername` es "Usuario"; una instalación nueva arranca con `default.username` del idioma ("Usuario" o "User", en `ui.New`). Nadie lo pide, así que los demás ven "Usuario", "Usuario (2)"…
- **Arreglo propuesto**:
  - Después de mostrar la ventana, si el nombre está vacío o es uno de fábrica ("Usuario", "User") y no se marcó "No volver a preguntar", sale un diálogo: "¿Cómo te llamás? Así te ven los demás en la sala", con la entrada, Guardar y el check.
  - Guardar hace lo mismo que Ajustes: `room.ClampUsername`, `updateConfig` + `saveConfig` y `selfNameLabel`.
  - Campo de config nuevo, del grupo "Otros" (lo escribe el diálogo, no Ajustes): por ejemplo `username_prompt_off`.
  - Función pura `needsUsernamePrompt(cfg)` con su test.
- **Docs**: README ("la primera vez te pide tu nombre").

### C12. Cruz para cerrar la imagen ampliada

- [x] **Pedido (Coffee)**: al abrir una imagen del chat falta la cruz para cerrarla.
  - **Hecho** (2026-11-13): `showLightbox` ([ui_components.go](internal/ui/ui_components.go)) suma un botón con `theme.CancelIcon()` en una capa encima de la imagen, arriba a la derecha (la imagen conserva todo el tamaño; el botón tiene fondo, así se ve sobre una imagen clara; lo vacío de la capa no toma clics). Esc: el contenido del popup es `lightboxView` (Focusable), que toma el foco al abrirse; en Fyne 2.6.3 cada popup tiene su propio manejador de foco y se va con él, así que no hay nada que soltar y el foco vuelve a lo que lo tenía. Tocar la imagen o afuera sigue cerrando. §3.G igual (decodificación en goroutine, `pop.Visible()`). Test `TestLightboxCloses` (Esc por el foco del canvas, la cruz, la imagen; el foco vuelve). **Quedó afuera**: nada del pedido (no hay flechas para pasar a otra imagen ni zoom, que no se pidieron).
- **Hoy**: `showLightbox` ([ui_components.go](internal/ui/ui_components.go)) es un `widget.PopUp` con la imagen (`newTappableImage`). Solo se cierra tocando la imagen o fuera de ella: no hay botón ni Esc.
- **Arreglo propuesto**: un botón con `theme.CancelIcon()` arriba a la derecha, encima de la imagen (en una capa sin layout, como el tooltip, o en un Border); Esc también la cierra. Respetar §3.G: la decodificación sigue en su goroutine y se chequea `pop.Visible()`.

### F6. "Detener" corta el sonido para todos

- [x] **Pedido (Coffee)**: cuando él o cualquiera toca "Detener" (o su atajo), que el sonido se corte para todos. A veces manda un audio largo por error y, si lo detiene él, los demás lo siguen escuchando.
  - **Hecho** (2026-11-13): al ver el plan, Coffee pidió **dos botones**: "Detener" queda como estaba (solo esta PC, con su atajo) y abajo "Detener para todos". `room.SoundStopMessage = "sound_stop"` ([protocol.go](internal/room/protocol.go)), sin datos, por `PacketChat` como `sound_play` (el host lo procesa una vez y lo reenvía; a quien lo mandó no le vuelve). [audio_bridge.go](internal/ui/audio_bridge.go): `stopSoundboard` sigue local (también lo usa la vista previa del recorte, que no tiene que cortar la sala); `cancelSounds` hace `Clear()` y vacía `pendingPlays` (un sonido que se estaba bajando se guarda al llegar pero no suena); `stopSoundsForAll` (botón, en una goroutine) corta acá y manda el mensaje. `handleChatPacket` ([network_sync.go](internal/ui/network_sync.go)) llama a `cancelSounds`. Botón `sound.stop_all` a todo el ancho debajo de "Detener" ([ui_soundboard.go](internal/ui/ui_soundboard.go)). No se avisa quién cortó (el mensaje no lleva identidad y el cliente lo recibe reenviado por el host). Test `TestSoundStopForEveryone` (host → socket → otra App: `PacketChat` con `sound_stop` y `pendingPlays` vacío en los dos). **Quedó afuera**: un atajo propio para "Detener para todos": con el botón de atajo al lado, "Detener para todos" no entra en los 280 px de la botonera (pide 175 px y quedan 126). Si se quiere, va con otro armado de esa fila.
- **Hoy** (antes de F6):
  - La botonera suena por un `audio.Player` aparte del Engine (`a.soundPlayer`, [audio_bridge.go](internal/ui/audio_bridge.go)). `stopSoundboard` solo hace `player.Clear()` en la PC propia.
  - Reproducir manda `room.SoundPlayMessage` por `PacketChat` (`triggerSound` → `sendChatPayload`; el host lo reenvía) y cada uno lo toca en `playIncomingSound`.
  - Si el audio se está bajando, queda en `pendingPlays` y suena cuando llega.
- **Arreglo propuesto**:
  - `room.SoundStopMessage = "sound_stop"`, por el mismo camino que `sound_play`.
  - `stopSoundboard` (botón y atajo) corta lo local y lo manda. Quien lo recibe hace `Clear()` y vacía `pendingPlays`, así un sonido que se estaba bajando no arranca después.
  - Cualquiera de la sala puede cortar para todos (así lo pidió).
  - Seguridad (§K): lo puede mandar cualquier participante, igual que `sound_play`, y no lleva datos. Es un mensaje nuevo en el sobre, sin compatibilidad hacia atrás (todos actualizan).
  - Decidir en el plan si se avisa quién lo cortó (en la barra de estado, no en el registro).
- **Tests**: el despacho del sobre. A mano, con dos instancias: cortar desde el que lo mandó y desde otro, y con un sonido que se está bajando.
- **Docs**: AGENT.md §3.J, README ("Detener corta para todos").

### F7. Ensordecer (no escuchar lo que llega sin salir de la llamada)

- [x] **Pedido (Coffee)**: un botón para dejar de escuchar el audio que llega, por ejemplo para escuchar otro audio sin salir de la llamada. Es lo único de esta tanda que cambia la funcionalidad a propósito.
  - **Hecho** (2026-11-13): decidido con Coffee al ver el plan: auriculares al lado del mic que se tachan en rojo como él; ensordecido no suenan **las voces ni la botonera** ("quiero escuchar un audio de WhatsApp y los demás tiran sonidos"); los sonidos de eventos siguen. `Engine.SetDeafened` ([engine.go](internal/audio/engine.go)): `mix` sigue leyendo cada voz de su jitter buffer (avanza, mide y marca quién habla) pero no la suma; el cambio es una rampa de un buffer (`voiceGain`) para que no haga clic; `__sfx__` no cambia. [ui_components.go](internal/ui/ui_components.go): `isDeafened`, `preDeafenMuted`, `toggleDeafen` / `setDeafened` / `setMuted`, íconos Material `headset` / `headset_off`, botón entre el mute y colgar. Como Discord: ensordecer mutea y al volver deja el mute de antes; tocar el mic ensordecido vuelve a escuchar y hablar. Los demás ven la insignia de mute de siempre (sin cambio de protocolo). Botonera: `playOrFetchSound` no hace nada ensordecido (el `sound_play` propio sale igual), `handleAssetChunk` no toca un pendiente, y al ensordecer `cancelSounds`. "Escuchar mi voz en vivo" no suena (lo corta el mute; `sendOutgoingPCM` también mira `isDeafened`). `startAudio` se lo aplica a cada Engine nuevo (antes y después de publicarlo). Tests `TestEngine_DeafenedMixIsSilent` (ceros exactos, el jitter buffer avanza sin acumular, los SFX suenan, la voz vuelve), `TestDeafenRestoresMute` y `TestDeafenedSoundboardIsSilent`. **Quedó afuera**: atajo de teclado; una insignia propia de "ensordecido" para los demás (pediría `Participant.Deafened`); la vista previa del recorte y las pruebas de Ajustes suenan igual (las toca uno a propósito).
- **Hoy** (antes de F7): solo existe el mute del micrófono (`isMuted`, `toggleMute`, `PacketRoomMute`, insignia en el avatar, AGENT.md §3.E2). Las voces suenan por el mix del Engine; la botonera, por el `audio.Player` aparte; los sonidos de eventos, por `Engine.Play` o ese player.
- **Arreglo propuesto**:
  - `App.isDeafened atomic.Bool` (solo en memoria, como el mute) y un botón con ícono de auriculares en la barra de arriba, al lado del mute.
  - `Engine.SetDeafened` (atómico): `mix` sigue leyendo cada stream (los jitter buffers siguen su tiempo y sus medidas, así no se acumula nada) pero saca silencio.
  - Mientras dure no suenan la botonera (tampoco los `sound_play` que llegan), los sonidos de eventos ni "Escuchar mi voz en vivo".
  - Decidir en el plan:
    - Como Discord: ensordecer también mutea el micrófono y al volver queda el mute de antes. Recomendado (si no escucha, que no hable sin saber qué pasa). La otra opción: solo cortar lo que llega.
    - Qué ven los demás: con lo de Discord alcanza la insignia de mute que ya existe (sin cambio de protocolo). Una insignia propia de "ensordecido" pide `Participant.Deafened` en el estado, como `Muted` (Hello, `takeOverHost`, reenvío en `failoverLoop`).
- **Tests**: `mix` ensordecido da ceros y el jitter buffer avanza igual (cgo); el toggle restaura el mute.
- **Docs**: AGENT.md §3.E2 (o una §3.E5 nueva), README.

### C18. Gráfico de lag por participante (debajo del nombre, oculto por defecto)

- [x] **Pedido (Coffee, 2026-11-13)**: un gráfico del lag de cada uno, para ver cuándo y cuánto, antes de la línea del registro (C13). Debajo de cada nombre, para mostrar u ocultar con un solo ícono: la leyenda "Mostrar detalle de lag" aparece solo al pasar el mouse. "No me gusta cuando se pierde info."
  - **Hecho** (2026-11-13):
    - **Historial** ([lag_history.go](internal/ui/lag_history.go), puro, con su mutex): por nombre, una muestra por casillero de 2 s (`lagSlot`). El Pong y el monitor de audio caen en el mismo y queda lo peor; una medida que llega tarde busca su casillero hasta 4 para atrás. Cada muestra ocupa 8 bytes (casillero, ms, pérdida y jitter; cada uno puede faltar). Ring de 24 h por persona (≈ 340 KB). Lo alimentan `updatePeerLag` y `updatePeerNetStats` ([peers.go](internal/ui/peers.go)). `lagColumns` (lo peor por columna; cada muestra cubre 3 s, así el Pong con jitter no inventa huecos y uno perdido sí deja uno), `summarizeLag` (máximo, promedio, pérdida máxima) y `lagScaleMS`. Las constantes del semáforo (80/200/400) y `lagColor` las comparte `formatPeerLag`.
    - **Cliente → host**: el Pong del host se atribuye por su **ID** (`updatePeerLag(id, …)`), no por dirección. Visto al hacer el plan: el host se anuncia con su IP pública y, si el invitado entró por LAN/VPN, el Pong venía de otra dirección y el chip del host quedaba en "… ms". Los Pong de otros (pings de la migración) solo cuentan para el ritmo de la pantalla.
    - **Mensaje nuevo** `PacketPeerLag = 20` ([lag.go](internal/room/lag.go), `EncodePeerLags` / `DecodePeerLags`): binario, `[cantidad][ID 8 bytes][ms u16]…`, como mucho 16 y largo exacto. `pingPeers` → `broadcastPeerLags` cada 2 s, solo con lo que midió el host en los últimos 6 s (`peerLagsToSend`); también vacío. El cliente lo acepta solo de su host (`handlePeerLags`; uno roto, una línea de log cada 10 s) y lo pone en el chip de los demás con "≈" (`lagViaHost`), sin guardarlo. El que no viene vuelve a "… ms".
    - **UI** ([ui_lag.go](internal/ui/ui_lag.go)): ícono "show_chart" a la derecha de "En sala" (`hintButton`, leyenda "Mostrar/Ocultar detalle de lag" al pasar el mouse; prendido, en el color de acento) que guarda `show_lag_graph` (config, grupo "Otros"). Tira de 110 × 16 px en el chip de cada uno (no en el propio), entre el avatar y el slider: barras con alto √(ms/500) y el color del semáforo, una marca roja arriba si hubo pérdida y una gris abajo si solo se midió la pérdida. Con el mouse, "21:34:12 · 350 ms · 8 % de pérdida". Al tocarla, el **gráfico grande** en un diálogo (decidido en el plan), con rango de 5 min, 1 h o toda la sesión (por defecto toda): franjas del semáforo, umbrales con su número, línea de ms en el color de cada tramo (la unión, en el peor de los dos), pérdida en barras desde abajo, huecos sin unir, el resumen y la muestra bajo el mouse en un renglón. Un cliente que mira a otro que no es el host ve que desde ahí solo se mide la pérdida. Se redibuja con `paintLag` (un `fyne.Do` cada 2 s desde `audioMonitorLoop`), solo lo que se ve y si hubo medidas nuevas o pasó un casillero.
    - Tests: `TestLagHistorySlotMergesBothSources`, `TestLagHistoryCap`, `TestLagHistoryPointsWindow`, `TestLagColumns` (sin huecos falsos con jitter y con más columnas que muestras; hueco real), `TestSummarizeLag`, `TestLagScaleMS`, `TestLagColorMatchesChip`, `TestPeerLagsRoundTrip`, `TestPeerLagsRejectsMalformed`, `TestPeerLagsOnlyFromHost`, `TestClientPongFromHostByID`, `TestPeerLagsToSend`, `TestLagToggleShowsStrips`, `TestLagPointText` y `TestDrawLagStrip`. Se miró con capturas del driver de test (la tira y el gráfico grande).
    - **Quedó afuera**: un corte total (sin Pong) se ve como hueco, no como lag (no hay muestra); el número que manda el host no se guarda (decidido); el diálogo no se cierra con Esc (los diálogos de Fyne 2.6.3 no lo atienden; tiene "Cerrar").
- **Hoy** (verificado el 2026-11-13; buscar por nombre):
  - **RTT**: Ping/Pong cada 2 s. El host mide a cada uno (`pingPeers`, [network.go](internal/ui/network.go)); el cliente, solo al host (`room.ActPing` desde `failoverLoop`). Llega a `updatePeerLag` ([peers.go](internal/ui/peers.go)), que pisa el valor: no queda historia.
  - **Pérdida y jitter** por emisor cada 2 s (`netStatsInterval`, `updatePeerNetStats` desde `audioMonitorLoop`). Los mide cada uno con su jitter buffer y también se pisan.
  - Como cliente, el chip de los que no son host dice "… ms" (`formatPeerLag` con `lag` 0): nadie le pasa ese número.
  - El estado de la sala (`broadcastRoomState`) se manda solo cuando cambia algo, no cada 2 s.
  - `room.ParticipantID` es un hash de nombre + IP:puerto: si alguien se reconecta desde otro puerto, tiene otro ID.
  - En disco ya hay un "modo debug": con nivel de log 1 se escriben los intervalos de 10 s con problemas; con nivel 2, todos.
- **Decidido con Coffee (2026-11-13)**:
  - Arranca **oculto**. Un solo ícono en la barra de participantes lo muestra u oculta. La leyenda va solo como tooltip ("Mostrar detalle de lag" / "Ocultar detalle de lag", con `a.tooltip` como `bindButton`). Se guarda en la config: grupo "Otros" (lo escribe el ícono, no Ajustes), por ejemplo `show_lag_graph`.
  - La tira del chip muestra los **últimos 5 min**.
  - Se guarda el historial de **toda la sesión en memoria**, sin archivos, y se borra **al cerrar la app**. Sobrevive a salir y volver a entrar a la sala, a la migración de host y a las reconexiones.
    - Costo: una muestra cada 2 s, unos 8 bytes, o sea ~15 KB/h por persona. 8 h con 5 personas son menos de 1 MB.
    - Tope de seguridad: 24 h por persona (ring buffer).
  - Cada uno guarda **solo lo que mide él**:
    - El host ve el historial completo de todos (ms, pérdida y jitter).
    - El cliente tiene el historial completo del host. De los demás, solo pérdida y jitter.
  - Los ms de los demás, del lado del cliente: el host manda **solo el número actual**, no el historial. El cliente lo muestra en el chip en lugar de "… ms" y no lo guarda en su historial.
- **Arreglo propuesto**:
  - **`lagHistory`** (archivo nuevo, por ejemplo `lag_history.go`):
    - Por participante, muestras {hora, ms, pérdida, jitter}, con los huecos marcados para no unir dos tramos con una línea.
    - Indexado por **nombre** (lo que ve el usuario), así una reconexión sigue en el mismo gráfico. Con C2 (misma fila), quien se reconecta desde la misma IP conserva su nombre, sin "(2)". Uno con otra IP y el mismo nombre ("Nombre (2)") es otra persona y tiene su propio gráfico.
    - Tiene su propio mutex. Lo alimentan `updatePeerLag` y `updatePeerNetStats`, no el hilo de UI.
    - Pura y testeada: agregar, tope, huecos, ventana de 5 min y resumen (máximo y promedio).
  - **La tira del chip** (`newPeerChip`):
    - Va debajo del nombre, del ancho del slider de volumen (110 px) y de unos 16 px de alto. El chip propio no lleva tira.
    - Una barrita por muestra: el alto son los ms y el color es el semáforo del chip, con las constantes compartidas con `formatPeerLag`. La pérdida va como una marca arriba.
    - Con el mouse encima, un tooltip con la muestra bajo el puntero: "21:34 · 350 ms · 8 % de pérdida".
    - Se dibuja con un solo `canvas.Raster` (o una imagen), no con 150 rectángulos, y se refresca solo si está visible.
  - **El gráfico grande**, al tocar la tira (ventana o popup, decidir en el plan):
    - Toda la sesión: línea de ms, barras de pérdida y franjas de fondo con los umbrales (80/200/400 ms).
    - Rango: 5 min, 1 h o todo.
    - Los tramos de lag de C13, marcados.
    - Dibujo propio, sin dependencias nuevas.
  - **Mensaje nuevo del host**:
    - Solo hacia los clientes, cada 2 s (desde `pingPeers`): ID → ms en enteros.
    - En un envelope chico, no en el estado de la sala, que se manda solo cuando cambia algo.
    - Seguridad (§K): se acepta solo del host actual. Sin compatibilidad hacia atrás (todos actualizan).
    - Decidir en el plan cómo marca el chip del cliente que es el lag de Pepe con el host (por ejemplo, "≈ 120 ms").
- **Tests**: `lagHistory` (agregar, tope de 24 h, huecos, ventana y resumen); muestra → color, con los mismos umbrales que el chip; el despacho del mensaje nuevo, aceptado solo del host.
- **Pruebas a mano**:
  - Un host y dos clientes. Cortar el Wi-Fi de uno y mirar la tira y el gráfico grande en el host y en el otro cliente.
  - Que reconecte y siga en el mismo gráfico.
  - Ocultar, cerrar la app y volver a abrirla: tiene que seguir oculto, y el historial, vacío.
- **Docs**: AGENT.md §3.A ("Measurement") y el mensaje nuevo en el protocolo; README ("detalle de lag").

### C13. Lag prolongado en el registro de actividad

- [x] **Pedido (Coffee)**: cuando hay mucho lag por un rato largo, que el registro diga desde cuándo. Fijarse a partir de cuánto deja de ser normal.
  - **Hecho** (2026-11-13): [lag_watch.go](internal/ui/lag_watch.go). `lagWatch`, pura, una por persona dentro del historial de C18: ve cada casillero de 2 s **cuando se cierra** (al abrirse el siguiente), así no mide nada nuevo y corre en la goroutine que alimentó el historial.
    - Umbrales confirmados con los logs del 11 al 13/11 (resúmenes de 5 min entre 0 y 1,5 % de pérdida; 12 de 217 intervalos con problemas pasan del 5 %; el RTT no se loguea): lag = más de 200 ms (`lagWarnMS`, el mismo del chip) o 5 % de pérdida (`lagLossPct`). Entra con 10 s seguidos (`lagSustain`) y sale con 10 s normales. Lo sin medir no cuenta, y más de 6 s sin muestras corta la racha.
    - Una línea al empezar: "Hay lag con Pepe desde las 21:34 (350 ms, 8 % de pérdida)." + la ayuda (misma persona → su Wi-Fi; todos → la tuya; el detalle, en el gráfico al lado de «En sala»). Otra al terminar: "Se normalizó la conexión con Pepe (duró 1 min 20 s; lo peor: 600 ms, 12 % de pérdida)." En el cliente, del host: "con el host (Pepe)". De los demás, el cliente usa solo la pérdida que mide él (decidido en el plan).
    - `reportLag` ([peers.go](internal/ui/peers.go)): `addActivity` + log en castellano; nunca `addSystemMessage`, sin barra de estado. Tope: un aviso por persona cada 5 min (`lagLineCooldown`, decidido con Coffee); los tramos del medio no avisan pero se guardan.
    - Cada tramo queda marcado en el gráfico grande: franja roja arriba y el fondo apenas sombreado (`lagHistory.episodes`, `lagEpisodeSpans`, `drawLagEpisodes`). La leyenda lo explica.
    - Tests `TestLagWatchEntersAndLeaves`, `TestLagWatchShortSpike`, `TestLagWatchDoesNotAlternate`, `TestLagWatchGapCutsStreak`, `TestLagWatchLossOnly`, `TestLagWatchCooldown`, `TestLagHistoryReportsOnSlotClose` y `TestLagEventMsg` (es y en, duraciones). Se miró con capturas: las dos líneas en el registro y los tramos en el gráfico.
    - **Quedó afuera**: un corte total no avisa como lag (es un hueco; lo cubren "Reconectando…" y "X salió"); si la persona se va con lag, el tramo queda abierto, sin línea de fin; la tira del chip no marca los tramos.
- **Hoy**:
  - El RTT de cada participante sale del Ping/Pong (`updatePeerLag`, [network.go](internal/ui/network.go)): el host mide a cada uno; el cliente, al host.
  - Pérdida y jitter por emisor, cada 2 s (`updatePeerNetStats`, desde `audioMonitorLoop`).
  - Solo lo muestra el chip del avatar (`formatPeerLag`: < 80 ms verde, ≤ 200 amarillo, ≤ 400 naranja, > 400 rojo, con colores fijos).
  - El log de nivel 1 escribe los intervalos de 10 s con problemas; el registro de actividad no dice nada.
- **Arreglo propuesto**: una máquina de estados pura por participante (`lagWatch`), testeada con un reloj falso.
  - "Lag" = RTT > 200 ms (naranja o peor en el chip) o pérdida ≥ 5 %, sostenido ≥ 10 s. Umbrales aceptados por Coffee (2026-11-13); si los logs reales muestran otra cosa, se ajustan en el plan. Las constantes se comparten con el chip.
  - Al empezar, **una** línea: "Hay lag con Pepe desde las 21:34 (350 ms, 8 % de pérdida)". Al volver a lo normal por ≥ 10 s, **otra**: "Se normalizó la conexión con Pepe (duró 1 min 20 s; lo peor: 600 ms, 12 %)". Con histéresis, para que no alterne.
  - Ayuda corta: si es siempre la misma persona, suele ser su Wi-Fi o alguien descargando en su red; si pasa con todos, es la propia.
  - El cliente solo mide al host: el texto dice "con el host (Pepe)".
  - **Va después de C18, en el mismo chat**: `lagWatch` lee las muestras de `lagHistory` (no mide nada nuevo). Los tramos que detecta se marcan en el gráfico grande, y la línea del registro remite al gráfico, por ejemplo "…; tocá su gráfico de lag para ver el detalle". En el cliente, para los demás, decidir en el plan si usa el número que manda el host (C18) más la pérdida que mide él, o solo la pérdida.
- **Tests**: la máquina entra, no entra por un pico corto, sale y no alterna. **Docs**: AGENT.md §3.A ("Measurement").

### C14. Problemas que le importan al usuario, en el registro y con cómo arreglarlos

- [x] **Pedido (Coffee)**: lo que ya detectamos y hoy va al log o a la barra de estado (se cambió el dispositivo, se perdió la conexión con el celular…) que se vea en el registro, y mejor si trae una ayuda corta para solucionarlo.
  - **Hecho** (2026-11-13): [problem_log.go](internal/ui/problem_log.go).
    - **Núcleo**: un `problemKind` por problema, cada uno con su ayuda (`problemHelp`). `problemLine` (pura) arma la línea: el texto de la barra + qué hacer. `problemCap` es el tope por tipo: la primera vez en la sesión y después como mucho una línea cada 5 min (`problemLineCooldown`), con mutex propio. `reportProblem` hace `addActivity` y deja la misma línea en castellano en el log ("Registro de actividad: …"); nunca `addSystemMessage`.
    - **Barra de estado**: sigue con su texto corto y la misma clave. El registro le suma la ayuda.
    - **Qué va al registro** (13 tipos):
      - Dispositivo que no está: `applyResolvedDevices`, micrófono y salida en una sola línea.
      - Audio caído: `checkAudioHealth`, solo si había Engine. Cuando vuelve, **una** línea `audio.ready` que dice con qué dispositivo quedó.
      - Audio que no abre: `startAudio`. `audio.unavailable` ya no lleva el error técnico (va al log) ni "Refresh Audio".
      - Celular desconectado: `phoneStatus`, el `onStatus` del servidor. Solo en una sala con el celular de entrada (al apagarlo uno mismo no avisa). Cuando vuelve, **una** línea `phone.connected`. También su servidor caído y el que no arranca al abrir la app.
      - Micrófono que pierde audio (menos de 48 frames/s) y PC que tira 5 o más frames (100 ms) por intervalo. Avisan con dos intervalos de 10 s seguidos con el mismo problema, una vez por racha (`sendProblem` + `sendWatch`, desde `audioMonitorLoop`). No con el celular de entrada ni muteado.
      - Cancelación de eco que falla: la goroutine de envío prende `a.echoFailed` y avisa `audioMonitorLoop`.
      - Captura de pantalla que falla al empezar, y config que no se guarda.
      - Al crear sala o conectar (pedido de Coffee al ver el plan): "Iniciar sala" sin puerto (`conn.port_failed`, el error técnico al log) e invitación inválida (`conn.invite_invalid`).
      - Host: llega un VoxMesh con otra clave (`conn.wrong_key`, "Alguien intenta entrar con una invitación vieja o de otra sala"). Lo cuenta `transport.UDP.WrongKey()`: magic y largo bien, pero no abre. La basura de un scanner no cuenta. Lo lee `rejectedPackets.check`, y la línea del log suma cuántos eran de VoxMesh.
    - **"(☰)"**: `room.join_failed_internet`, `invite.port_failed` y `lag.legend`. Fyne dibuja el ☰ con la fuente de símbolos.
    - **Tests**: `TestProblemLine`, `TestProblemCap`, `TestSendProblem`, `TestSendWatch` y `TestReportProblemInActivityLog` (con el registro de verdad: barra sin ayuda, registro con ayuda, el tope, muteado no avisa); `WrongKey` en `TestReceiveDropsWrongKey` y `TestReceiveSurvivesJunkAndClosedPeer`. Se miró con una captura (las líneas en el registro y el ☰).
    - **Quedó afuera**:
      - Las fallas del servidor del celular desde Ajustes (se ven en la barra de Ajustes).
      - La pantalla que falla en medio de una transmisión (escritorio seguro): sigue en el log.
      - La cola o un frame lento del envío: solo atrasan, quedan en el log.
      - `conn.key_failed` y el UDP del cliente, que casi no pasan (el cliente ya reintenta en un puerto libre).
      - El motivo de `audio.dropped` sale del Engine en castellano, también con la app en inglés.
- **Hoy** (verificado el 2026-11-13; buscar por nombre):
  - Dispositivo que no está, se usa el predeterminado: `applyResolvedDevices` ([audio_bridge.go](internal/ui/audio_bridge.go)), `audio.input_missing` / `audio.output_missing`. Va a la barra de estado y como ERROR al log.
  - Se cayó el audio y se reabre (USB o Bluetooth desconectado, volver de suspensión): [audio_recovery.go](internal/ui/audio_recovery.go), `audio.dropped` / `audio.reloaded`.
  - Audio no disponible: `audio.unavailable`. Dice "Probá con Refresh Audio", pero el botón ahora se llama "Reiniciar audio" (texto viejo: arreglarlo acá).
  - Celular conectado o desconectado, y fallas de su servidor: `phone.connected`, `phone.disconnected`, `status.phone_server_start_failed`.
  - Micrófono que pierde audio (menos de 48 frames/s o frames descartados; típico del Bluetooth): `captureInterval.problem` en [audio_monitor.go](internal/ui/audio_monitor.go), solo al log.
  - Cancelación de eco que falla y se apaga sola: solo al log.
  - Captura de pantalla que falla: `status.screen_failed`.
  - Config que no se puede guardar: `status.config_save_failed`.
  - Host: llegan paquetes que no abren con la clave (`rejectedPackets`, "Paquetes descartados" en el log), o sea, alguien usa una invitación vieja. Sirve para R8.
- **Arreglo propuesto**:
  - Cada uno, una línea en el registro con qué hacer, según la regla de arriba (por ejemplo: la primera vez en la sesión y después como mucho una cada 5 min por tipo). Misma clave que la barra de estado, con el patrón de `reportHostNet` (C6).
  - Ayudas de ejemplo: dispositivo → "Elegí otro en Ajustes, o conectalo y tocá Reiniciar audio"; micrófono que pierde audio → "Si es Bluetooth, probá con cable o acercalo"; paquetes con otra clave → "Alguien intenta entrar con una invitación vieja: pasale una nueva".
  - Lo que ya va al registro (migración de host) y los avisos normales (sonido agregado, perfil guardado) quedan como están.
  - Por C17 (el registro se ve con ☰), los textos que mandan a mirarlo dicen dónde está: "mirá el registro de actividad (☰)". Esto incluye `room.join_failed_internet` (R8) y el renglón rojo de C8.
- **Tests**: evento → texto (función pura) y el tope por tipo.
- **Docs**: AGENT.md §3.L (qué va al registro), README ("mirá el registro de actividad").

### F8. Que un grito no deje sordo a nadie

- [x] **Pedido (Coffee, 2026-11-13)**: que a partir de la versión que viene el modo grito venga apagado por defecto para todos ("uno grita, me deja sordo y le bajo el volumen"). O, si se puede, que en modo grito se baje más por defecto.
  - **Hecho** (2026-11-14), opción B: [duck.go](internal/audio/duck.go) (`shoutDucker`, sin cgo), uno por emisor de red dentro de `audioStream`; `Engine.mix` lo corre primero sobre lo decodificado, antes del filtro de salida y del volumen individual, fuera del lock, sin asignaciones (lo toca solo el callback, como `fx`). No depende de "Aplicar filtro de salida". Detección **por nivel** (no `FlagShout`: sirve aunque el que grita tenga el modo grito apagado o "Sin filtros"): RMS de cada bloque del callback → envolvente (sube en 5 ms, baja al instante) → baja "Cuánto" × (envolvente − umbral) / 6 dB, entre 0 y "Cuánto" (una voz que apenas toca el umbral baja poco) → se sostiene 300 ms entre sílabas y vuelve en 150 ms, siempre en rampa a lo largo del bloque. Mientras no hay nada que bajar, el audio sale bit a bit igual. Params de Salida (sección nueva "Gritos que llegan", la primera): `shout_duck_enabled` (1), `shout_duck_db` (−10 dB, de −30 a 0) y `shout_duck_threshold_db` (−14 dBFS, de −30 a −6); los tiempos y la transición son fijos (`AudioShoutDuck*`, [audio.go](internal/config/audio.go)). El umbral salió de medir la voz sintética de los tests: a su nivel (pico −10,5 dBFS) la envolvente no pasa de −17,5; un grito (la misma voz +15 dB, limitada al techo de envío) pasa −14 en el 82–85 % de los bloques con voz. Tests: `TestShoutDuck_NormalVoiceUntouched` (voz normal y 2,5 dB más fuerte, con bloques de 441/480/512/960: igual bit a bit), `TestShoutDuck_ShoutGoesDown` (nunca más que "Cuánto"; la mitad de los bloques baja ≥ 9 dB), `TestShoutDuck_RampsHoldsAndReleases`, `TestShoutDuck_OffLeavesAudioAlone`, `TestEngine_ShoutDuckLowersLoudVoiceOnly` (también con el volumen individual en 30 %) y `TestEngine_MixWithVoiceDoesNotAllocate` (cero asignaciones con una voz bajando). `syntheticVoice` pasó de `aec_test.go` a `voice_test.go` (sin tags). **Quedó afuera**: cuánto y desde qué volumen se ajustan probando con un grito real (pruebas manuales); no hay contador de gritos bajados en el log.
- **Hoy** (verificado el 2026-11-13; buscar por nombre):
  - **Qué es el modo grito.** Es de quien habla: está en el perfil de **Entrada** (`shout_enabled`, default 1; [shout.go](internal/audio/shout.go)).
    - **No sube el volumen.** Mientras dura, mezcla un 20 % de la voz original (`shout_dry_mix`) con la salida de RNNoise, para que el grito no suene robótico ni cortado.
    - Marca los paquetes con `transport.FlagShout`: es el aro rojo del avatar (`markPeerShouting`).
  - **Apagarlo no baja el grito.** Un grito llega igual de fuerte, casi al techo de envío (`send_ceiling_db`, −1,7 dBFS), muy por encima de una voz normal. Lo único que cambia es cómo suena el grito, y además el que escucha pierde la marca `FlagShout`.
  - **Lo que tiene el que escucha:**
    - Limitadores de pico: `output_limiter_db`, dentro del filtro de salida (apagado por defecto), y `bus_limiter_db` (−1,1 dBFS). Evitan que sature; no bajan un grito.
    - El slider de volumen por persona, que baja todo, también cuando esa persona habla normal.
  - **Los defaults:** "Default" se vuelve a aplicar al arrancar (`config.ApplyDefaultProfiles`). Un cambio de default llega solo a quien usa "Default"; los perfiles guardados ("Personalizado"…) conservan su valor. Para cambiarlo "para todos" habría que migrar los perfiles guardados, y la Visión dice sin migraciones.
- **Decidido con Coffee (2026-11-13)**: opción **B**, con su propio slider en Salida, aparte del volumen por persona. El modo grito sigue prendido por defecto: la A queda descartada. Coffee: "mandale, es un slider más". Cuánto bajar y desde qué volumen se ajustan probando con un grito real.
- **Opciones**:
  - **A. `shout_enabled` con default 0** (lo pedido primero). Llega solo a quien tiene la Entrada en Default, no baja el volumen (ver arriba) y se pierde la marca del grito. **Descartada.**
  - **B. El que escucha baja los gritos que le llegan (recomendado).** Va en el perfil de **Salida**: "Bajar los gritos que llegan" (prendido por defecto) y "Cuánto" (dB, por ejemplo −10).
    - Va por persona, en la mezcla del Engine, antes del volumen individual, con rampa para que no haga clic.
    - Depende solo de quien escucha: sirve aunque el que grita tenga otra versión u otro perfil. Sin cambio de protocolo.
    - **Detección, a decidir en el plan:**
      - Por **nivel** de lo que llega (recomendado): un compresor por stream que baja lo que pasa de un umbral. Sirve siempre. El ataque tiene que ser rápido para que el primer grito no pase entero.
      - Por **`FlagShout`**: es más barato, pero solo sirve si el que grita tiene el modo grito prendido.
    - Si se hace por nivel, el umbral por defecto no puede tocar una voz normal. Medirlo con grabaciones reales (log nivel 2) o con las muestras de los tests de audio.
  - A y B no se excluyen: el modo grito puede seguir prendido (suena mejor y marca el aro rojo) y el volumen lo controla quien escucha.
- **Tests**: frames fuertes → la ganancia baja con rampa; una voz normal no cambia; la opción apagada no toca nada. A mano: alguien grita y se compara con la opción prendida y apagada, también con el volumen individual al 100 % y al 300 %.
- **Docs**: AGENT.md §3.A (cadena de recepción), README ("los gritos se bajan solos; se cambia en Ajustes → Salida").

### C19. Plantilla fija "Sin filtros" en Entrada y Salida (para probar los filtros de a uno)

- [x] **Pedido (Coffee, 2026-11-13)**: una configuración "Sin filtros" que nunca se pueda pisar al tocar "Guardar".
  - **Hecho** (2026-11-14): [profiles.go](internal/config/profiles.go): `BuiltinProfiles` (Default y `NoFiltersProfileName` = "Sin filtros", clave fija que se guarda así en `voxmesh.json` en cualquier idioma; en pantalla `profile.no_filters`: "Sin filtros" / "No filters"), `CanonicalProfileName` / `IsBuiltinProfile` (también por el nombre traducido, sin distinguir mayúsculas), `BuiltinValues` (`noFiltersValues`: lo de la lista de abajo, más los dos checks generales prendidos), `ProfileForBuiltinChanges` (puro: si hay que crear "Personalizado") y `ApplyBuiltinProfiles` (antes `ApplyDefaultProfiles`; al arrancar también deja el nombre con la clave). `ValidateProfileName` rechaza los nombres de fábrica (`ErrProfileNameReserved`, `err.profile.reserved`); `Values`, `Delete` y `List` los tratan como fijos. Un perfil guardado antes con uno de esos nombres se ignora (no se lista; si estaba activo, queda el de fábrica): sin migración. [ui_audio_params.go](internal/ui/ui_audio_params.go): selector con los dos de fábrica primero (`profileLabel` / `profileFromLabel`), Renombrar y Borrar deshabilitados en los dos, Guardar pide un nombre, ayuda debajo de la barra con "Sin filtros" elegido (en Entrada avisa que sin VAD ni puerta se manda todo el tiempo), `saveBuiltinChangesAsProfile` (el Guardar de Ajustes compara con el de fábrica activo) y `applyLoaded` (lo que hace cargar un perfil, separado de la goroutine). Tests: `TestNoFiltersProfileValues`, `TestNoFiltersValuesAreParams`, `TestBuiltinProfileNames`, `TestSavedProfileWithBuiltinNameIsIgnored`, `TestProfileForBuiltinChanges`, `TestApplyBuiltinProfilesOnlyTouchesBuiltins` y `TestProfileBarNoFilters` (UI). Se miró con capturas en es y en. **Quedó afuera**: nada de lo pedido. Si se cambia algo y se guarda, que pida un nombre. Es una plantilla única para partir de cero y modificar desde ahí.
  - **Default**: la configuración actual (a Coffee le gusta).
  - **Sin filtros**: para que alguien vaya probando los filtros uno por uno.
- **Hoy** (verificado el 2026-11-13; buscar por nombre):
  - Los perfiles son archivos en `<AppDir>/perfiles/<entrada|salida>/` ([profiles.go](internal/config/profiles.go)). El único fijo es "Default" (`DefaultProfileName`): no es un archivo, sino los valores de `Params`. No se puede renombrar ni borrar.
    - "Guardar" sobre él pide un nombre (`profileBar.save` → `saveAs`, [ui_audio_params.go](internal/ui/ui_audio_params.go)).
    - Si se guardan los Ajustes con cambios sobre él, se crea "Personalizado" (`saveDefaultChangesAsProfile`).
    - `ValidateProfileName` rechaza su nombre.
  - O sea: lo que se pide ya existe para Default. Falta un **segundo perfil fijo**.
  - El Default de Salida ya tiene el check general apagado (`output_filter_enabled` = 0: ganancia, pasa-altos, pasa-bajos y limitador solo corren con ese check). En Salida, "Sin filtros" se diferencia por ser un punto de partida para probar (ver abajo) y por F8.
- **Decidido con Coffee (2026-11-13)**: en los **dos** tipos, Entrada y Salida.
- **Arreglo propuesto**:
  - Generalizar el perfil fijo: una lista de perfiles de fábrica por tipo (Default y "Sin filtros"), cada uno con sus valores.
    - "Sin filtros" = Default con cada filtro apagado, para prenderlos de a uno y escuchar qué hace cada uno:
      - **Entrada**: modo grito, cancelación de eco, RNNoise, VAD, compuerta, pasa-altos, pasa-bajos, notch, compresor, expansor y limitador apagados; ganancia 0 dB.
      - **Salida**: pasa-altos, pasa-bajos, limitador y "bajar los gritos" (F8) apagados; ganancia 0 dB.
      - El check general (`audio_filter_enabled`, `output_filter_enabled`) queda **prendido**, así prender un solo filtro ya se nota. Si no, el usuario prende uno y no pasa nada. Confirmarlo en el plan.
      - **Ojo**: sin VAD ni compuerta, el micrófono manda todo el tiempo, también el ruido. La ayuda del perfil tiene que decirlo.
    - El techo de envío, el códec, el buffer del celular, el buffer de recepción y el techo de la mezcla quedan como en Default: no son filtros y protegen.
  - Lo que hoy pregunta `== DefaultProfileName` pasa a una función (`isBuiltinProfile`). "Sin filtros":
    - No es un archivo; no se renombra ni se borra.
    - "Guardar" pide un nombre.
    - Si se guardan los Ajustes con cambios sobre él, crea "Personalizado".
    - `ValidateProfileName` rechaza su nombre.
    - `ApplyDefaultProfiles` también lo vuelve a aplicar al arrancar.
  - En la config se guarda con una clave fija que no dependa del idioma, y se muestra traducido ("Sin filtros" / "No filters"). Decidirlo en el plan, junto con qué pasa si ya hay un perfil guardado con ese nombre.
- **Tests**: los valores del perfil (todo filtro apagado y lo demás igual que Default), el nombre rechazado, Guardar pide un nombre y se vuelve a aplicar al arrancar.
- **Docs**: AGENT.md (perfiles), README.

### C22. Debajo de mi nombre, un slider de "desde qué volumen me toma la voz"

- [x] **Pedido (Coffee, 2026-11-13)**: así como hay un slider de volumen debajo de cada participante, que haya uno debajo del propio (el de quien usa la PC). No es el volumen propio: es desde qué volumen la entrada empieza a tomar la voz.
  - **Hecho** (2026-11-14): [ui_self_gate.go](internal/ui/ui_self_gate.go): `newSelfGate` en el chip propio (`newPeerChip`): `gateSlider` de 110 px (embebe `widget.Slider`, leyenda con `a.tooltip` como `hintButton`) con el rango y el paso de `threshold_db`, el valor al lado ("-42 dB") y detrás una `levelBar` de 10 px a lo largo de la línea del slider (`sliderTrackLayout`) con `postDB` en la escala del slider (`selfGatePos`: la barra pasa el botón justo cuando el nivel pasa el umbral), verde si se manda y gris si no o muteado. Mientras se arrastra se aplica en vivo (`setSelfGate`, que también **prende la puerta** si estaba apagada, p. ej. con "Sin filtros"; decidido en el plan); al soltar, `saveSelfGate` (goroutine, de a una): perfil propio activo → se guarda ese; de fábrica → "Personalizado" por `config.ProfileForBuiltinChanges` (el mismo camino que el Guardar de Ajustes, C19) y aviso **solo en la barra de estado** al crearlo. Con "Aplicar filtro de voz" apagado la puerta no corre: el slider va deshabilitado y la leyenda dice por qué. [input_meter.go](internal/ui/input_meter.go): `reportMeter` le da el mismo frame a `levelListener` (Ajustes) y a `selfGateMeter` (mientras existe el chip propio, `setSelfGateWidgets`); `onSelfGateMeter` pinta a lo sumo cada 50 ms con un `fyne.Do` y no pinta con Ajustes abierto. Sincronía: Ajustes reemplaza la vista principal (nunca se ven los dos); al cerrarlo (Guardar o Descartar) el chip toma el valor sin `SetValue` (en Fyne 2.6.3 también dispara `OnChangeEnded`). Tests: `TestSelfGateSlider`, `TestReportMeterFansOut`, `TestSelfGateMeterPaint`. Se miró con capturas en es y en. **Quedó afuera**: la línea del umbral en la barrita (la tapa el botón del slider, que ya es el umbral).
- **Hoy** (verificado el 2026-11-13; buscar por nombre):
  - Los chips de los demás tienen `newPeerVolumeSlider` (110 px, 0–300 %). El propio no tiene nada (`newPeerChip` con `peer.isSelf`) y solo existe dentro de una sala.
  - **Cuándo se manda la voz** (`PrepareOutgoingFrames`, [rnnoise.go](internal/audio/rnnoise.go)): tienen que estar de acuerdo dos cosas, cada una con su hold:
    - el VAD de RNNoise (`vad_threshold`, "Exigencia del VAD", de 0 a 1, default 0,5);
    - la puerta de ruido (`noise_gate_enabled`, default 1; `threshold_db`, "Volumen mínimo para mandar", default −42 dBFS), que mira el nivel ya limpio.
  - El medidor de Ajustes ya muestra ese nivel: la barra "Después de filtros", con una línea blanca en el umbral (`level_bar.go`). En la sala, cada frame enviado pasa por `reportMeter` con `postDB`, pero llega a un solo listener (`a.levelListener`, el de Ajustes).
  - Los dos parámetros son del perfil de Entrada. Si el activo es Default, un cambio se pierde al reiniciar salvo que se guarde como "Personalizado" (`saveDefaultChangesAsProfile`; ver C19).
- **Arreglo propuesto**:
  - **El slider controla `threshold_db`**, el volumen mínimo para mandar. Es un número de volumen y se entiende; la "exigencia" del VAD no. El VAD queda como está, en Ajustes.
  - **Como la "sensibilidad de entrada" de Discord:**
    - Detrás del slider, una barrita con el nivel en vivo (`postDB`) y el umbral marcado. Va en verde cuando se manda y en gris si estás muteado. Así se ve "acá me toma, acá no".
    - Se reusa `level_bar.go`.
    - El listener pasa a admitir más de uno (Ajustes y el chip). Sin un `fyne.Do` por frame: a lo sumo 20 refrescos por segundo, como el medidor de Ajustes.
  - Mismo ancho que el de los demás (110 px), con el valor al lado ("−42 dB"). Tooltip: "Desde qué volumen te toma la voz. Más a la derecha = tenés que hablar más fuerte."
  - **Al soltarlo** (`OnChangeEnded`): se aplica en vivo y se guarda en el perfil activo. Con Default activo, se crea "Personalizado" por el mismo camino que el Guardar de Ajustes: Default no se pisa (C19).
  - Sincronizado con Ajustes: si está abierto, su slider de "Volumen mínimo para mandar" se mueve igual, y al revés.
  - **Decidir en el plan:**
    - Si la puerta de ruido está apagada en el perfil (por ejemplo, con "Sin filtros" de C19): mover el slider la prende, o el slider se ve deshabilitado con una leyenda.
    - Si se avisa "Se creó el perfil Personalizado" la primera vez.
- **Tests**: slider → valor del parámetro (escala, rango y paso de `Params`); con Default activo se crea "Personalizado"; el listener múltiple. A mano: hablar bajo y fuerte mientras se mueve el slider, mirar la barrita, reiniciar y que el valor siga.
- **Docs**: README ("el slider debajo de tu nombre"), AGENT.md (§3.A, envío).

### C23. Sliders de volumen con curva logarítmica

- [x] **Pedido (Coffee, 2026-11-13)**: que los sliders de volumen sean logarítmicos.
  - **Hecho** (2026-11-14): [volume_curve.go](internal/ui/volume_curve.go) (`volumeCurve`, puro): la perilla se mueve en posición 0–1 (paso 0,01) y se convierte en ganancia en dB: 0 = silencio; de ahí al 100 % parejo desde −40 dB; un "clic" de ±5 % del recorrido que da exactamente 100 % (y no se guarda, como antes); del clic al máximo (`peer_volume_max`, 300 % de fábrica) con los mismos dB por tramo (≈ 0,55 dB por 1 %). El rótulo sigue en % (decidido en el plan: para el usuario común; 100 % = sin cambio). **Se sigue guardando la ganancia real**: lo guardado vuelve a la misma ganancia. Usada en `newPeerVolumeSlider` ([ui_components.go](internal/ui/ui_components.go)) y en el de la botonera (`buildSoundPanel`, [ui_soundboard.go](internal/ui/ui_soundboard.go)). De paso, con OK de Coffee: **la botonera en 0 sonaba al 100 %** (`buildSoundPanel`, `playSoundPCM` y `config.Load` cambiaban ≤ 0,01 por 1,0); ahora 0 es silencio (`Load` solo lo lleva a 0–1) y, pedido de Coffee, llegar a 0 prende "Bloquear sonidos ajenos" y subirlo lo apaga (salvo que el usuario haya tocado el check en el medio; solo en la sesión). Tests: `TestVolumeCurve` (extremos, clic, ida y vuelta de lo guardado con el slider viejo, sube siempre, mismos dB a los dos lados), `TestSoundboardVolumeSlider` y `TestLoadSoundboardVolume`. Se miró con capturas. **Quedó afuera**: una marca visible en el 100 % (Fyne 2.6.3 no tiene marcas en el slider; el clic alcanza); si la app arranca con la botonera en 0, subirla no desbloquea (no se sabe si el bloqueo lo puso el usuario).
- **Hoy**: son lineales. El valor multiplica la señal tal cual:
  - el volumen por persona (`newPeerVolumeSlider` → `setPeerVolume` → `Engine.SetStreamVolume`, de 0 a 300 %, guardado por nombre en `PeerVolumes`);
  - el de la botonera (`SoundboardVolume`, `applyVolume`).

  El oído percibe el volumen de forma logarítmica: el 50 % se escucha apenas más bajo (−6 dB) y casi todo el cambio que se nota queda entre 0 y 20 %.
- **Arreglo propuesto**:
  - El slider se mueve en una escala perceptual: la posición pasa a ganancia con una curva (por ejemplo, en dB o con una potencia).
  - **Se sigue guardando la ganancia real**: los volúmenes ya guardados no cambian, solo dónde queda la perilla.
  - El rótulo puede seguir en % o pasar a mostrar dB (decidir en el plan). El 0 sigue siendo silencio y el 100 % queda en un lugar fácil de encontrar (por ejemplo, con una marca o un "clic" al pasar).
  - Los sliders en dB (el "cuánto bajar los gritos" de F8, el umbral de C22) ya son logarítmicos y no cambian.
- **Tests**: posición ↔ ganancia (ida y vuelta, extremos y 100 %); un valor guardado vuelve a la misma ganancia.
- **Docs**: AGENT.md (sliders de volumen).

### C20. Un atajo para prender y apagar los atajos de la botonera (para jugar con teclado)

- [x] **Pedido (Coffee, 2026-11-13)**: poder desactivar los atajos de la botonera. Jugando al Counter-Strike con teclado molestan; con joystick no. Primero pidió un check; después, mejor un **atajo** que los prenda y los apague, para hacerlo sin salir del juego; al final, un **botón con su atajo al lado**, como "Detener".
  - **Hecho** (2026-11-14): decidido con Coffee al ver el plan: el aviso suena **siempre** (también con "Sonidos de eventos" apagado: dentro del juego es la única señal) y la tecla repetida se rechaza en **los tres** diálogos de atajo.
    - Config, grupo "Otros" ([config.go](internal/config/config.go)): `toggle_hotkeys_bind` (sin tecla por defecto) y `sound_hotkeys_off`. Se guarda y sigue igual al reabrir la app.
    - [hotkey_windows.go](internal/ui/hotkey_windows.go): `hotkeyBinds(cfg)` (pura) arma la lista del poller; apagados queda solo el atajo que los prende (también el de "Detener" se apaga) y la config conserva los demás. `boundHotkey` lleva su acción (`hotkeySound` / `hotkeyStop` / `hotkeyToggle`); el poller llama a `toggleSoundHotkeys` con `fyne.Do`. Los dos diálogos de atajo (sonido y "Detener") eran copias: ahora los tres usan `showBindDialog`. `bindConflict` (pura) compara la tecla ya parseada ("Ctrl + K" = "ctrl+k") con el de "Detener", el de los atajos y los sonidos de la botonera actual (los de otra sala no suenan, no cuentan); si choca, el diálogo dice "F3 ya lo usa «Detener». Elegí otra tecla." y queda abierto. Los atajos repetidos que ya estaban guardados no se tocan.
    - [ui_soundboard.go](internal/ui/ui_soundboard.go): fila nueva debajo de "Detener para todos", con la forma de la de "Detener": el botón con ícono de teclado (Material "keyboard", rojo apagados, como el mic muteado) y el estado, **"Atajos: sí" / "Atajos: no"** ("Hotkeys: on/off"; "Atajos prendidos" pide 158 px y quedan 126), y a la derecha su botón de atajo. `toggleSoundHotkeys` cambia el estado (`setSoundHotkeysOff`: guarda, publica y actualiza el botón), escribe "Atajos de la botonera apagados/prendidos." en la barra de estado y el registro de actividad y, en una goroutine, toca el aviso (`playCue`, [audio_bridge.go](internal/ui/audio_bridge.go): el Engine o la salida de la botonera, sin mirar `event_sounds_enabled`; `playEventSound` lo usa con ese check). Sonidos `audio.SFXHotkeysOn` (G5→C6) y `SFXHotkeysOff` (C6→G5), más agudos y cortos que los de entrar y salir ([sfx.go](internal/audio/sfx.go)).
    - Tests `TestHotkeyBinds`, `TestBindConflict` y `TestToggleSoundHotkeys` ([hotkey_windows_test.go](internal/ui/hotkey_windows_test.go)).
    - **Quedó afuera**: el ítem del menú de la bandeja va con C21. Apareció C25 (el atajo "Numpad +" nunca anda).
- **Hoy** (verificado el 2026-11-13; buscar por nombre):
  - Los atajos se guardan en la config: `SoundBinds` (sonido → tecla) y `StopSoundBind` ("Detener").
  - `pollHotkeys` ([hotkey_windows.go](internal/ui/hotkey_windows.go)) lee el teclado cada 25 ms con `GetAsyncKeyState`, sobre la lista que arma `publishHotkeys` (`activeHotkeys`).
    - No se queda con la tecla: el juego la recibe igual y además suena el sonido.
    - Solo se saltea una tecla suelta mientras se escribe **en VoxMesh** (`isTypingInApp`). En un juego no hay nada que los frene.
  - No hay forma de apagarlos sin borrar los atajos uno por uno.
  - El atajo de "Detener" se elige con `showStopSoundBindDialog` y se guarda en `StopSoundBind`. No vi que el diálogo de atajos rechace una tecla que ya usa otro atajo.
  - Hay sonidos de eventos locales (`playEventSound`, `audio.SFXConnect` / `SFXDisconnect`).
- **Arreglo propuesto**:
  - **El atajo "Prender/apagar atajos"**:
    - Se elige igual que el de "Detener": su botón de atajo al lado (`newBindButton`) y el mismo diálogo. Se guarda en un campo nuevo, por ejemplo `toggle_hotkeys_bind`. Sin tecla por defecto, para no chocar con un juego.
    - En `pollHotkeys` es el **único** que anda con los atajos apagados.
    - Apagados, `publishHotkeys` publica solo ese. Los demás quedan guardados y vuelven al prenderlos.
  - Apaga **todos**, también el de "Detener" (decidido con Coffee, 2026-11-13).
  - El estado (prendidos o apagados) va en un campo de config del grupo "Otros", por ejemplo `sound_hotkeys_off`. Se guarda: sigue igual al volver a abrir la app. Sin migración.
  - **Aviso al cambiarlo**: dentro del juego la ventana no se ve, así que suena un sonido corto **local**, distinto para prender y para apagar (como `playEventSound`). También una línea en el registro de actividad ("Atajos de la botonera apagados / prendidos"). Decidir en el plan qué sonidos se usan.
  - **Decidido con Coffee (2026-11-13): un botón, no un check**, en el mismo lugar y con la misma forma que "Detener" (`stopRow` en `buildSoundPanel`).
    - Una fila nueva: a la izquierda, el botón que prende y apaga los atajos, que muestra el estado (por ejemplo "Atajos: prendidos" / "Atajos: apagados", con ícono). A la derecha, su botón de atajo.
    - Se actualiza solo (`fyne.Do`) cuando se cambia con el atajo.
    - Sirve también sin haber elegido un atajo.
  - Si la tecla elegida ya la usa otro atajo (un sonido o "Detener"), el diálogo lo avisa y no la acepta. Decidir en el plan si se extiende a todos los atajos.
  - **Decidido con Coffee (2026-11-13)**: los botones y sus atajos se ven igual estando apagados.
- **Tests**:
  - `publishHotkeys` apagado → solo queda el de prender/apagar, y la config conserva los demás. Prendido → todos.
  - La tecla repetida se rechaza.
  - A mano:
    - En un juego o en el Bloc de notas: apretar el atajo → suena el aviso y los demás atajos no suenan; apretarlo de nuevo → vuelven.
    - El botón se actualiza solo.
    - Cerrar y abrir la app → sigue como quedó.
- **Docs**: README ("atajos de la botonera"), AGENT.md si hay una sección de atajos.

### C21. La X manda la app al área de notificaciones (con menú)

- [x] **Pedido (Coffee, 2026-11-13)**: al tocar la X, que VoxMesh se vaya al área de notificaciones (al lado del reloj). Configurable en Ajustes y prendido por defecto. Con clic derecho en el ícono: colgar, mutear, ensordecer y salir.
  - **Hecho** (2026-11-14): decidido con Coffee al ver el plan: el ícono **siempre** (Fyne 2.6.3 no lo puede sacar y volver a poner), "Salir" del menú **sale sin preguntar**, el aviso es un diálogo y el menú suma "Atajos de la botonera" (C20).
    - Verificado en Fyne 2.6.3 / systray v1.11.0: el clic izquierdo y el derecho abren el mismo menú; Fyne agrega su "Quit" (`d.Quit`) salvo que el último ítem tenga `IsQuit` con acción propia; la ventana "SystrayMonitor" llama a `d.Quit` al cerrarse. **Corrección**: esa ventana **no** se cierra al apagar Windows (Windows no manda WM_CLOSE y GLFW ignora WM_QUERYENDSESSION/WM_ENDSESSION); se cierra con `taskkill` sin `/F`, instaladores o WM_QUIT. Al apagar Windows la app muere sin cerrar la sala, como antes: R10.
    - Config: `close_to_tray` (LiveSettings, `Params`, prendido, en Ajustes → Interfaz y Sistema; aplica enseguida) y `tray_hint_off` (grupo "Otros").
    - [tray.go](internal/ui/tray.go) (puro): `trayMenuEntries` (Abrir VoxMesh · Colgar · Silenciar micrófono ✓ · Ensordecer ✓ · Atajos de la botonera ✓ · — · Salir con `IsQuit`), `closeActionFor` (la X) y `trayMenuOpen` (watchdog).
    - [ui_tray.go](internal/ui/ui_tray.go): `setupTray` en `Run`, en la goroutine principal y antes de `application.Run` (Fyne crea ahí su ventana oculta y solo arranca la bandeja si ya existía); un solo `*fyne.Menu` al que se le cambian los `Items` (Fyne recarga el primer menú cuando la bandeja termina de arrancar); leyenda "VoxMesh" con `systray.SetTooltip` (`fyne.io/systray` pasa a dependencia directa); el cierre de "SystrayMonitor" pasa a `quitApp`. `refreshTrayMenu` lo llaman `refreshMuteButton`, `refreshDeafenButton`, `setSoundHotkeysOff` y `setHangupEnabled` ([ui_components.go](internal/ui/ui_components.go), reemplaza los 4 lugares que prendían y apagaban el botón de colgar). `closeWindow` (la X), `hideToTray` (guarda el estado de la ventana, `Unfocus` para que no se salteen los atajos de una tecla, `Hide`), `showMainWindow` (`Show` + `restoreWindow` + `RequestFocus`), `quitApp` (cierra la sala como la X, sin preguntar) y `showTrayHint` ("VoxMesh sigue abierto", "No volver a mostrar", "Salir de VoxMesh" | "Entendido").
    - [ui_main.go](internal/ui/ui_main.go): `quit` corre una sola vez (`quitting`) y, con la ventana oculta, no vuelve a leer su estado. [watchdog.go](internal/ui/watchdog.go): con el menú del ícono abierto (modal en el hilo de UI) no avisa bloqueo.
    - Una sola instancia: [instance_windows.go](internal/winproc/instance_windows.go) `SingleInstance` (mutex `Local\VoxMesh-<hash de la carpeta del exe>` + evento "-show"), `RequestShow` (`AllowSetForegroundWindow` + `SetEvent`) y `OnShowRequest`. Por carpeta: `voxmesh.json` está al lado del exe y las copias en otras carpetas siguen andando juntas. [main.go](cmd/voxmesh/main.go): después de `WaitForParent`; la segunda muestra la primera y sale antes de `ui.New`; con `--wait-pid` (`updater.Relaunched`) nunca cede.
    - Caminos de salida: X con la opción → oculta, sigue en la sala; X sin la opción → como antes; "Salir" del menú, "Salir de VoxMesh" del aviso y "SystrayMonitor" → `quit(stopConnectionAsync)` (Leave, regla UPnP, `flushConfig`); updater → sin cambios; apagar Windows → R10.
    - Tests `TestTrayMenuEntries`, `TestCloseActionFor`, `TestTrayMenuOpen`, `TestTrayMenuFollowsState` (bandeja falsa: mute, ensordecer, atajos y colgar desde la ventana rehacen el menú; Salir con `IsQuit` y acción), `TestTrayHint` ([tray_test.go](internal/ui/tray_test.go)), `TestInstanceName` y `TestSingleInstance` ([instance_windows_test.go](internal/winproc/instance_windows_test.go)). Los tests que guardan la config usan `newAppWithOwnConfig` (espera el guardado antes de borrar la carpeta, el problema de T3).
    - **Quedó afuera**: el ícono no cambia con el mute (no se pidió); mientras el menú del ícono está abierto la ventana no se repinta (límite de Fyne 2.6.3 + systray); apagar Windows (R10); el foco que queda en una entrada con Alt+Tab (C24).
- **Hoy** (verificado el 2026-11-13; buscar por nombre):
  - **La X de la ventana principal** (`SetCloseIntercept`, [ui_main.go](internal/ui/ui_main.go)):
    - En una sala pregunta si salir (`main.quit.*`) y sale con `a.quit(a.stopConnectionAsync(), nil)`. Fuera de una sala, `a.quit(nil, nil)`.
    - `a.quit` oculta la ventana, espera hasta 3 s a que se cierre la sala (Leave y regla UPnP), guarda la config y cierra Fyne.
    - Nunca `window.Close()` (AGENT.md §3.G, trampa de Fyne 2.6.3).
  - **Fyne 2.6.3 trae ícono de bandeja**: `desktop.App.SetSystemTrayMenu` y `SetSystemTrayIcon`, con `fyne.io/systray` v1.11.0. El menú admite ítems con tilde (`Checked`) y deshabilitados (`Disabled`); para cambiarlos se vuelve a llamar a `SetSystemTrayMenu`.
  - **Limitaciones de esa versión:**
    - En Windows, el clic izquierdo y el derecho abren **el mismo menú**: no hay "clic izquierdo abre la ventana". Por eso el menú lleva primero "Abrir VoxMesh".
    - Si el último ítem no es el de salir, Fyne agrega su propio "Quit", que cierra sin pasar por `a.quit`: no manda el Leave ni borra la regla UPnP. Nuestro "Salir" tiene que llevar `IsQuit = true` y su propia acción.
    - Fyne crea una ventana oculta ("SystrayMonitor") cuyo cierre llama a `Quit` (por ejemplo, al apagar Windows). Revisar que ese camino también cierre la sala bien.
  - `Show` y `RequestFocus` no sacan una ventana minimizada: para volver está `restoreWindow` (`window_state_windows.go`, `SW_RESTORE`).
  - `toggleMute` y `toggleDeafen` ([ui_components.go](internal/ui/ui_components.go)) ya existen. Colgar es lo mismo que "Salir de la sala" o "Parar sala" (`stopConnectionAsync`).
  - **No hay control de una sola instancia.** Con la ventana oculta es fácil volver a abrir VoxMesh desde el escritorio y quedar con dos: los atajos sonarían dos veces y las dos escribirían `voxmesh.json`.
- **Arreglo propuesto**:
  - Param `close_to_tray` (default 1) en "Interfaz y Sistema": "Al cerrar la ventana, seguir abierto en el área de notificaciones". Apagado, la X hace lo de hoy.
  - Con la opción prendida, la X oculta la ventana (`rememberWindowState` + `Hide`) sin preguntar y sin salir de la sala.
  - **Menú del ícono:**
    1. "Abrir VoxMesh": muestra y restaura la ventana.
    2. "Colgar": deshabilitado fuera de una sala. Hace lo mismo que el botón de la ventana.
    3. "Silenciar micrófono": con tilde si está muteado.
    4. "Ensordecer": con tilde si está ensordecido.
    5. Separador y "Salir": cierra todo por `a.quit` (con `stopConnectionAsync` si hay sala).
  - El menú se rehace (`fyne.Do` + `SetSystemTrayMenu`) cuando cambian la sala, el mute o el ensordecer, también si se cambian desde la ventana o con un atajo.
  - **Avisar la primera vez que se oculta** que la app sigue abierta y cómo salir. Para usuarios comunes es clave: si no, creen que se cerró. Puede ser un diálogo con "No volver a mostrar" o una notificación de Windows (`SendNotification` de Fyne corre un script de PowerShell).
  - **Una sola instancia** (hace falta con esto): si se abre VoxMesh con otra ya abierta, la nueva muestra la que estaba y se cierra. Por ejemplo, con un mutex con nombre y un aviso a la que está abierta. Ojo con el updater: el proceso nuevo espera al viejo (`--wait-pid`) antes de tomar el mutex.
  - **Decidir en el plan:**
    - El ícono siempre que la app está abierta, o solo con la ventana oculta.
    - "Salir" desde el menú: si pregunta estando en una sala (habría que mostrar la ventana para el diálogo) o sale directo, porque lo eligió a propósito.
    - Cómo es el aviso de la primera vez.
  - **A considerar (no lo pidió)**: sumar al menú el botón de C20 ("Atajos de la botonera", con tilde) y que el ícono cambie si está muteado o ensordecido.
- **Tests**: el armado del menú como función pura (estado → ítems con etiquetas, tildes y deshabilitados); la fila de `Params`. A mano:
  - La X con la opción prendida y apagada.
  - Cada ítem del menú, dentro y fuera de una sala.
  - Mute y ensordecer cambiados desde la ventana se ven en el menú.
  - "Salir" en una sala: el otro ve que se fue.
  - Volver a abrir el exe con la ventana oculta: aparece la que estaba.
  - Actualizar con la app oculta.
- **Docs**: README ("al cerrar queda junto al reloj; para salir, clic derecho → Salir"), AGENT.md §3.G (cierre y bandeja).

### C15. Paletas de colores (naranja para lo que resalta)

- [x] **Pedido (Coffee)**: que lo que resalta tenga el tono naranja (le gusta), y que en Ajustes se pueda cambiar la paleta: una por defecto (la elige después, jugando con las plantillas), algunas plantillas y la posibilidad de crear paletas propias.
  - **Hecho** (2026-11-14). Decidido con Coffee al arrancar: **Default = todos los colores de hoy** (y es la predeterminada); el naranja va en una plantilla. [palette.go](internal/ui/palette.go): todo color de la app es un nombre del tema (los de Fyne y los propios `colorName…`: tarjeta, chip, texto secundario, hablando, grito, inactivo, avatar y su inicial, pantalla compartida, medidores, coincidencia del chat), `paletteEntries` (30 colores: clave del archivo ↔ nombre ↔ texto), las de fábrica **Default** (exacta: los `voxColors` de antes + el tema oscuro de Fyne), **Naranja** (Default con el resaltado `#fb8c00`) y **Clara** (base clara, texto oscuro, el mismo naranja, con medidores, avatar e insignias para fondo claro), `voxTheme` (paleta activa sobre Fyne; una que define el primario también deriva foco, selección, links y el texto sobre el primario, el de más contraste: oscuro sobre el naranja), y `themedRect` (rectángulo o círculo que vuelve a leer su color en cada `Refresh`). Se reemplazaron los ~38 colores fijos: `uiCard`, leyenda, insignias, banner, chips, resaltado del chat, LEDs, indicador de voz, `levelBar`, medidores y onda de la botonera leen el tema. [ui_palette.go](internal/ui/ui_palette.go): `applyPalette` (sin reiniciar: `SetTheme` refresca cada widget y se rearman los chips), fila en Ajustes → Interfaz (elegir se aplica al instante; Descartar la revierte), editor con un selector de color por cada uno, "Paleta clara", **Aplicar** (los colores no cambian mientras se eligen), Guardar / Guardar como… / Cerrar (sin guardar vuelve la activa); Renombrar y Borrar solo en las propias. [palettes.go](internal/config/palettes.go): un JSON por paleta en `<AppDir>/paletas/` (como los perfiles; un color que falta o no se entiende sale de la base; las que se llaman como una de fábrica, en cualquier idioma, se ignoran). Config: `active_palette` (LiveSettings). Tests: `TestDefaultPaletteKeepsTheLook`, `TestPaletteTemplates`, `TestPalettesAreReadable` (contraste de texto y fondo en cada una), `TestPaletteFileRoundTrip`, `TestPaletteNames`, `TestApplyPaletteLive`, `TestPaletteSaveLoadRenameDelete`, `TestPaletteNames` (config). Se miró con capturas de las tres. **Quedó afuera**: los colores del semáforo del lag (verde, amarillo, naranja, rojo) quedan fijos porque significan lo mismo en cualquier paleta; los LEDs del celular usan éxito/error del tema (casi igual que antes); un diálogo que ya estaba abierto (gráfico de lag, recorte) toma la paleta al volver a abrirlo.
- **Hoy**:
  - `voxTheme` ([ui_theme.go](internal/ui/ui_theme.go)) es fijo y siempre oscuro (`voxColors`). Lo que resalta usa el `Primary` azul de Fyne: checks, "Conectar", botones `HighImportance`, sliders, foco.
  - Hay unos 38 colores fijos fuera del tema: `ui_components.go` (12: `speakingColor`, `shoutColor`, `idleColor`, `mutedTextColor`, los del lag…), `ui_settings.go` (10, medidores), `level_bar.go` (2), `ui_soundboard.go` (3) y `ui_chat.go` (1).
  - Además, 11 `theme.Color(…)` se leen al crear objetos de canvas (tooltip, renglones de "Invitar", insignias…), así que no seguirían un cambio de tema.
- **Arreglo propuesto**:
  - Una paleta es un juego de colores con nombre: resaltado/primario, fondo, tarjeta, chip, botón, entrada, texto, éxito/advertencia/error, hablando/grito/inactivo y medidores. Todo pasa por nombres de color del tema (`colorName…`), y los colores fijos de arriba se convierten en nombres.
  - Paletas de fábrica: la actual con el resaltado naranja como "Default" (Coffee elige después cuál queda) y 2 o 3 plantillas.
  - Paletas propias: un JSON por paleta en `<AppDir>/paletas/`, con el mismo patrón que los perfiles de audio ([profiles.go](internal/config/profiles.go): Default de fábrica que no se pisa, Guardar como, Renombrar, Borrar). Editor en Ajustes: la lista de colores con `dialog.NewColorPicker`.
  - Config: el nombre de la paleta activa (texto, como `ActiveInputProfile`).
  - **Decidido con Coffee (2026-11-13)**:
    - **Sin reiniciar.** Elegir una paleta de la lista se aplica al instante. En el editor, un botón "Aplicar": los colores no cambian mientras se elige cada uno.
      - Cuesta lo mismo que aplicarlo en vivo: lo caro es no reiniciar (`Settings().SetTheme` + refrescar los objetos que leen el color al crearse).
    - **Puede haber paletas claras.** Una plantilla "Clara" es la de ahora "invertida": fondos claros y texto oscuro, con el mismo naranja.
      - No es una inversión literal: el naranja invertido sería azul.
      - Cambia la regla "siempre oscuro" de AGENT.md §3.D.
      - Revisar que se lean los colores que hoy asumen fondo oscuro (medidores, chips, insignias).
- **Tests**: cargar y guardar paletas (puro, como los perfiles); que todo nombre de color que se usa tenga valor en cada paleta.
- **Docs**: AGENT.md §3.D ("Look": nada de colores fijos), README.

### C26. El aviso de la bandeja dice "Ocultar" en vez de "Entendido"

- [x] **Pedido (Coffee, 2026-11-14)**: cambiar el "Entendido" del aviso que sale al cerrar la app (queda en el área de notificaciones) por "Ocultar".
  - **Hecho** (2026-11-14): `tray.hint_ok` pasa a `tray.hint_hide` ("Ocultar" / "Hide"); el botón (`trayHint.hide`) hace lo mismo que antes (oculta la ventana, `hideToTray`). `TestTrayHint` toca "Ocultar".
- **Hoy**: `showTrayHint` ([ui_tray.go](internal/ui/ui_tray.go)): "Salir de VoxMesh" y "Entendido" (`tray.hint_ok`), que oculta la ventana.

### C27. Clic izquierdo en el ícono de la bandeja abre la app

- [x] **Pedido (Coffee, 2026-11-14)**: con la app en el área de notificaciones, un clic común la abre de una; el derecho muestra las opciones (que ya andan bien).
  - **Hecho** (2026-11-14): [tray_click_windows.go](internal/ui/tray_click_windows.go): `hookTrayLeftClick` (lo llama `setupTray`) busca la ventana oculta de systray (clase "SystrayClass", la del hilo actual: el principal la creó dentro de `SetSystemTrayMenu`) y le cambia el procedimiento de ventana (`SetWindowLongPtrW`): el clic izquierdo (`WM_USER+1` con `WM_LBUTTONUP`, `isTrayLeftClick`) → `fyne.Do(showMainWindow)`; todo lo demás sigue en el de systray (el derecho abre el menú como antes). Si no la encuentra, una línea en el log y el clic izquierdo abre el menú. Fuera de Windows, nada (`tray_click_other.go`). El aviso de la primera vez y el README dicen "clic derecho y Salir". Test `TestIsTrayLeftClick`; **falta probarlo a mano** (no se puede crear el ícono en los tests).
- **Hoy**: fyne.io/systray v1.11.0 (el de Fyne 2.6.3) abre el menú con los dos clics (`WM_LBUTTONUP` y `WM_RBUTTONUP` en su `wndProc`) y no deja cambiarlo; Fyne no expone nada.

### C28. El log más nuevo nunca se borra

- [ ] **Pedido (Coffee, 2026-11-14)**: que el último archivo de log quede siempre, aunque pasen días.
- **Hoy**: `logging` borra los logs de más de 48 h al abrir el del día: si la app no se abrió en tres días, al abrirla se borra el último, justo el que hay que mandar cuando algo falló.

### C29. El renglón del chat se ilumina al pasar el mouse por su ícono de copiar

- [ ] **Pedido (Coffee, 2026-11-14)**: al poner el mouse sobre el ícono de copiar de un mensaje, que se ilumine ese renglón para saber bien qué se copia. Decidido con Coffee: mientras el mouse está encima, iluminado con poca opacidad; al copiar, un destello.
- **Hoy**: `buildChatMessageObject` ([ui_chat.go](internal/ui/ui_chat.go)): el ícono va al borde derecho y el texto a la izquierda; con renglones cortos no se ve a cuál pertenece.

### C16. Lavado de cara de la app, sin perder el estilo clásico

- [ ] **Pedido (Coffee)**: los botones se ven raros. Hacerle un lavado de cara a toda la app, junto con C15, sin romper el estilo "viejo" o "clásico": que siga pareciendo una app de escritorio tipo XP. Pensado para usuarios comunes.
- **Hoy** (captura del 2026-11-13, Ajustes con la ventana maximizada a 1920 px):
  - La tarjeta "Interfaz y Sistema" ocupa todo el ancho.
  - Los tres botones (carpeta de datos, logs, actualizaciones) se estiran de punta a punta: C4 los puso uno debajo del otro en un VBox.
  - Las ayudas grises cruzan toda la pantalla.
  - "Cancelar" y "Guardar" quedan chiquitos en la esquina de abajo, y "Guardar" no se distingue (no tiene `HighImportance`).
- **Arreglo propuesto** (con capturas de antes y después; el driver de test de Fyne puede generar PNG con `Capture()`):
  - Ajustes con un ancho máximo de contenido: una columna centrada, o dos columnas en pantallas anchas.
  - Botones de ancho natural (alineados a la izquierda o en una grilla de 2 o 3), y "Guardar" como botón principal, con el color de resaltado.
  - Espaciados y títulos de tarjeta parejos. Revisar con el mismo criterio la barra de arriba, el chat y la botonera.
  - "Clásico tipo XP": botones con borde, ícono y texto; tarjetas; menús. Nada de rediseño estilo web o celular. Si hace falta, pedirle a Coffee una captura de referencia de lo que quiere decir.
  - Antes de empezar, juntar con Coffee lo que a su amigo le pareció "una paja".
  - Respetar lo que ya hay: §3.D (`TestMainViewIgnoresLongText`, columnas de ancho fijo, colores del tema) y C4 (con la ventana a ~960 px, Ajustes no la agranda).
- **Dependencias**: C15 (colores). Va al final para cubrir los botones nuevos de C10 y F7.

### C17. El registro de actividad queda oculto dentro de la sala

- [x] **Decidido con Coffee (2026-11-13): queda como está.** Dentro de la sala el registro se ve abriendo ☰, y así está bien. Lo único que queda va en C14: que los textos que mandan a mirar el registro digan dónde está.
- **Visto en C8 (2026-11-13)**: el "Contexto común" de arriba dice que el registro "siempre está a la vista (columna izquierda, también dentro de la sala)", pero `openHost` y `connectClient` ([ui_connection.go](internal/ui/ui_connection.go)) llaman a `hideConnectionPanels` ([ui_components.go](internal/ui/ui_components.go)), que oculta `a.connectionPanel`: la columna entera, con las tarjetas Host y Cliente **y** la del registro (`activityCard`). Dentro de la sala solo se ve con ☰. Fue a propósito en C3 ("el panel izquierdo se oculta solo"), antes de que el registro fuera el lugar donde la app dice qué hacer (C6, R8, C8, C9, C13, C14).
- **Por qué importa**: los avisos del host ("no se pudo abrir el puerto…", "se cumplió el tiempo del puerto…") y el de R8 al que entra ("pedile que mire su registro de actividad") apuntan a algo que no está en pantalla. C8 lo emparcha: el renglón rojo de "Invitar" muestra la columna.
- **Opciones que se vieron (descartadas el 2026-11-13)**: al entrar a la sala ocultar solo las tarjetas Host y Cliente y dejar el registro; o mostrar la columna sola cuando llega una línea con un problema. Revisar el ancho (`fixedWidthLayout`, `connectionPanelWidth`) y C16.

# Prioridad: invitar sin errores (2026-11-11)

**Contexto común.** Al apretar "Iniciar sala", `openHost` ([ui_connection.go](internal/ui/ui_connection.go)) publica primero la IP **local** como dirección de la sala (`netinfo.LocalIPv4`, la de la ruta por defecto) y lanza `configureHostNetwork` en segundo plano: UPnP (`netinfo.MapUDP`, hasta 15 s) y después la IP pública (`netinfo.PublicIP`, ipify, hasta 4 s). Recién ahí la dirección pasa a ser la pública. Lo mismo al quedar de host por migración (`takeOverHost`, [network_failover.go](internal/ui/network_failover.go)). Mientras tanto, el menú "Invitar" (`showInviteMenu` → `hostInviteOptions`, [ui_invite.go](internal/ui/ui_invite.go)) solo muestra "Internet · …" si la dirección actual pasa `isInternetAddr`: durante esos segundos ofrece **solo** las LAN/VPN y no avisa que falta la de Internet. Captura del 2026-11-11: el menú con "LAN/VPN · Radmin VPN · 26.77.45.242:47830" y "LAN/VPN · Ethernet 4 · 192.168.1.37:47830", sin "Internet". El único aviso es "Sala abierta en UDP %d; buscando IP pública..." en la barra de estado, fácil de no ver (y se puede apagar con `show_status_bar`).

**Internet es el camino principal (decisión de Coffee).** Sin el check de C7, la gente va a usar "Internet", que es lo cómodo. LAN/VPN es una alternativa para quien no puede recibir conexiones por internet (router sin UPnP, CGNAT) y sí o sí quiere usar la app. Por eso: mientras se obtiene la IP pública no se sugiere LAN/VPN (es cuestión de esperar), y cuando internet falla, los avisos dicen primero cómo arreglarlo y recién al final mencionan LAN/VPN como última opción. **(2026-11-13: C8 cambia esto: las LAN/VPN se muestran por defecto; Internet sigue primero.)**

### C6. "Invitar": "Obteniendo IP pública…" en naranja mientras se abre el puerto

- [x] **Pedido (Coffee)**: mientras obtiene la IP pública y abre el puerto, que aparezca en naranja "Obteniendo IP pública…".
  - **Hecho** (2026-11-12): estado `a.hostNet` (`atomic.Pointer[hostNetwork]`, [host_network.go](internal/ui/host_network.go)): `hostNetFinding` → `hostNetReady` / `hostNetNoUPnP` / `hostNetNoIP`, con la IP pública guardada en el mismo estado. Lo crean `openHost` y `takeOverHost` antes de `hostMode`; `configureHostNetwork` lo cierra con `CompareAndSwap`: si la sala se cerró o dejó de ser host mientras respondían el router o ipify, no escribe nada (antes ponía la dirección pública en una sala ya cerrada). Vuelve a nil en `stopConnectionAsync` y `stepDown`. Menú: popup propio (`showInvitePopup` + `inviteRow`, [ui_invite.go](internal/ui/ui_invite.go)) con `canvas.Text` de los colores del tema, porque `fyne.Menu` pinta todos los textos igual y un ítem deshabilitado queda gris con ícono incluido. Botón "Invitar": reloj naranja mientras busca (`refreshInviteButton`). Registro: dos líneas por inicio de sala o por quedar de host (`reportHostNet`, solo `addActivity`), con las mismas claves que la barra de estado (`conn.net_finding/ready/no_upnp/no_ip`; se borraron `conn.open_finding_ip`, `upnp_no_public_ip`, `no_upnp_no_ip`, `share_no_upnp` y `ready_upnp`). Al copiar con el puerto sin abrir, `invite.copied_port_closed`. Funciones puras con tests: `inviteOptions`, `copiesDirectly`, `hostNetMsg` (`TestInviteOptions`, `TestInviteOptionsPortClosed`, `TestHostNetMsg`). De paso: al quedar de host por migración, "Invitar" ofrecía como "Internet" la IP del host viejo, que seguía en el endpoint hasta que terminaba `configureHostNetwork`. **Quedó afuera**: el popup abierto no se rearma solo cuando cambia el estado (el reloj se va y al reabrir ya dice "Internet"), y el botón no cambia en las fallas (solo mientras busca).
- **Arreglo propuesto** (confirmar en el plan):
  - **Estado de la red del host** en `App`, atómico (por ejemplo `hostNet atomic.Int32`): `buscando` → `listo` (UPnP + IP pública), `sinUPnP` (hay IP pública pero el puerto no se abrió: hoy `conn.share_no_upnp`) o `sinIP` (falló ipify: hoy `conn.upnp_no_public_ip` / `conn.no_upnp_no_ip`). Lo pone en `buscando` quien lanza `configureHostNetwork` (`openHost` y `takeOverHost`); `configureHostNetwork` lo cierra en cada salida (también en el `return` de "la sala se cerró mientras el router respondía"); vuelve a cero al cerrar la sala o dejar de ser host.
  - **La IP pública se guarda aparte**, con el estado. No deducir "Internet" de `isInternetAddr(endpoint)`: Radmin usa `26.0.0.0/8`, que es pública, y si la ruta por defecto pasara por la VPN y fallara ipify, el menú ofrecería la de Radmin como "Internet".
  - **Menú Invitar del host**, primer renglón según el estado: `buscando` → "Obteniendo IP pública…" en naranja, que no copia nada; `listo` → "Internet · IP:puerto" como hoy; `sinUPnP` → "Internet · IP:puerto (puerto sin abrir)" en naranja y, al copiar, el aviso de abrir a mano el puerto UDP; `sinIP` → "Internet: no se pudo obtener la IP pública" en rojo. El cliente no cambia (copia la dirección del host tal como la ve él).
  - **Indicador fuera del menú**: mientras dure `buscando`, algo naranja al lado de "Invitar" en la barra de arriba (un punto o el texto corto) que se va solo al terminar, para verlo sin abrir el menú. Refresco con `fyne.Do` al cambiar el estado.
  - **Color**: el naranja de advertencia del tema (`theme.ColorNameWarning`, [ui_theme.go](internal/ui/ui_theme.go)). `fyne.MenuItem` no tiene color de texto: ver si alcanza con un ícono de advertencia o si hace falta un popup propio con `canvas.Text` de color (como la leyenda de C3, `tooltip`, capa propia). Lo más simple que se vea naranja.
  - **Registro de actividad** (pedido de Coffee): que se vea cómo quedó la red del host, **sin llenarlo**: una línea al empezar y una con el resultado, nada más (sin reintentos ni detalles; eso queda en el log). Con `a.addActivity` ([ui_chat.go](internal/ui/ui_chat.go)), solo local: **no** `addSystemMessage`, que siendo host lo difundiría a la sala, y estos avisos llevan datos de la red de esta PC. Textos tentativos (que cada falla diga qué hacer):
    - Al empezar: "Obteniendo IP pública y abriendo el puerto…".
    - `listo`: "Sala lista por internet: ya podés invitar."
    - `sinUPnP`: "No se pudo abrir el puerto automáticamente (UPnP). Para que entren por internet: activá UPnP en el router, o abrí a mano el puerto UDP N hacia esta PC (IP local X). Si no se puede, que cree la sala otro, o usen LAN/VPN (Ajustes)." La IP local va solo acá, porque es lo que pide el router para abrir el puerto a mano.
    - `sinIP`: "No se pudo obtener la IP pública. Revisá tu conexión a internet y volvé a iniciar la sala."
    - CGNAT (si R8 lo detecta): "Tu proveedor de internet (CGNAT) no deja que entren a tu sala por internet. Que cree la sala otro, o usen LAN/VPN (Ajustes)."
    - Al quedar de host por migración, lo mismo (el que queda de host puede tener otro router).
    - Mismas claves de i18n que la barra de estado cuando se pueda (`conn.share_no_upnp`, `conn.upnp_no_public_ip`, `conn.no_upnp_no_ip`, `conn.ready_upnp`): hoy dicen cosas parecidas con otras palabras. Unificar en vez de tener dos versiones.
- **Tests**: sacar a una función pura el armado de las opciones a partir de (estado, IP pública, puerto, interfaces, opción de C7); hoy `hostInviteOptions` lee la red adentro (`netinfo.LocalIPv4s`). Todos los estados × C7 prendido/apagado. A mano: Iniciar sala y abrir Invitar enseguida (naranja) y esperar (pasa a "Internet"); router sin UPnP o con UPnP apagado ("puerto sin abrir"); sin internet (rojo); migración: el que queda de host ve lo mismo.
- **Docs**: README (qué significa el naranja) y AGENT.md, donde describe "Invitar" (§3, F3).

### C7. Ocultar las IPs LAN/VPN en "Invitar" (opción en Ajustes para mostrarlas)

- [x] **Pedido (Coffee)**: ocultar las IPs locales y que haya un check en Ajustes para mostrarlas.
  - **Hecho** (2026-11-12): `show_lan_invites` (default 0): campo en `LiveSettings`, fila en `config.Params` y `param.show_lan_invites.label/.help`. El check está en "Interfaz y Sistema", debajo de la barra de estado. Apagado: `inviteOptions` no lista interfaces y nunca copia la IP local (se sacó el fallback `options = endpoint`). Mientras busca, solo el renglón naranja. Con una falla (`sinUPnP` o `sinIP`) agrega abajo el renglón gris "Otra opción: invitar por LAN/VPN (activalo en Ajustes)". Prendido: Internet arriba y todas las LAN/VPN abajo, también mientras busca. El socket sigue escuchando en todas las interfaces. El texto que mandaba a "la dirección local de la sala" ya no existe: `conn.net_no_upnp` nombra "LAN/VPN (Ajustes)". README: el naranja, el check y qué hacer si no se abre el puerto.
- **Hoy** el host ve un renglón por cada IPv4 de cada interfaz (`netinfo.LocalIPv4s`): Ethernet, Wi-Fi, Radmin, Tailscale, ZeroTier y también adaptadores virtuales (Hyper-V, WSL, VirtualBox). Para el uso normal (internet) sobran y llevan a copiar la que no sirve (ver R8).
- **Arreglo**:
  - Opción nueva en Ajustes → "Interfaz y Sistema": **"Mostrar direcciones LAN/VPN al invitar"**, apagada por defecto. Fila en `config.Params` (por ejemplo `show_lan_invites`, default 0) siguiendo AGENT.md §3.I "Adding a setting", como `show_status_bar`.
  - Apagada: `hostInviteOptions` no lista interfaces. Con "Internet" listo y nada más se copia directo, sin menú (ya pasa con `len(options) == 1`). Si "Internet" no está listo, **no copiar la IP local**: con `buscando`, solo el renglón naranja de C6 (es esperar unos segundos, no sugerir LAN/VPN); con una falla (`sinIP`, CGNAT), el renglón de C6 y abajo uno gris "Otra opción: invitar por LAN/VPN (activalo en Ajustes)". Sacar el fallback de hoy (`options = endpoint` cuando no hay nada), que con la sala recién abierta es la IP local.
  - Prendida: como hoy ("Internet" arriba y las LAN/VPN abajo).
  - Revisar los textos que mandan a "la dirección local" (`conn.upnp_no_public_ip`: "usa la dirección local de la sala"): que nombren la opción.
- **Tests**: los de la función pura de C6 con la opción prendida y apagada. A mano: apagada, no aparece ninguna 192.168.x ni 26.x en el menú; prendida, aparecen; queda guardada al reabrir la app.
- **Docs**: README (para invitar por Radmin, Tailscale o ZeroTier, activar la opción).

### R8. "Código incorrecto o host inalcanzable" al entrar a la sala de un amigo

- [ ] **Qué pasó (2026-11-11)**: un amigo creó la sala y otro, al pegar la invitación, vio a los 10 s "Código incorrecto o host inalcanzable." (registro de actividad, 21:36). Sin logs todavía.
- **Por qué el mensaje no ayuda**: desde S4 el host descarta en silencio lo que no abre con la clave, así que el que entra no distingue "invitación de otra sesión" de "al host no le llega nada". El `ActGiveUp` sale de `Failover` a los `Join` (10 s) sin ningún estado ([failover.go](internal/room/failover.go), `JoinFailedKey`).
- **Hipótesis** (de más a menos probable):
  1. **Copió una invitación LAN/VPN** (`192.168.x` o Radmin `26.x`), porque "Internet" todavía no aparecía o por elegir mal. Solo sirve en la misma red o VPN. **Con C7 casi deja de pasar**: las LAN/VPN están ocultas por defecto, así que solo puede copiar una quien activó el check a propósito, y mientras se obtiene la IP pública el menú no copia nada. Las que quedan son las fallas del camino por internet (2 a 4).
  2. **UPnP no funcionó** en el router del host: "Internet" se ofrece igual, con el puerto cerrado. Hoy solo lo dice la barra de estado (`conn.share_no_upnp`). Con C6 lo marca el menú en naranja y el registro de actividad dice qué hacer.
  3. **CGNAT del proveedor del host**: UPnP "anda" pero la IP WAN del router es `100.64.x` o privada, distinta de la de ipify, y nadie de afuera llega. ~~Hoy no se detecta~~ (detectado desde el 2026-11-12, ver "Arreglo"). Arreglo posible: pedirle `GetExternalIPAddress` al mismo cliente UPnP que abrió el puerto (`netinfo.Mapping`) y compararla con la de ipify; si es privada/CGNAT o distinta → estado nuevo en C6: "Tu proveedor no deja recibir conexiones (CGNAT): usá LAN/VPN (Radmin, Tailscale)". A largo plazo, F4.
  4. **Firewall de Windows** en la PC del host: la app no crea regla y depende del cartel de Windows la primera vez. Si lo cerró, o la red está como "Pública" y solo permitió "Privada", no entra nada por UDP. Comprobar: `netsh advfirewall firewall show rule name=all verbose | findstr /i voxmesh`, o Seguridad de Windows → Firewall → "Permitir una aplicación". Crear la regla desde la app pide admin: solo si resulta ser la causa; si no, una línea en el README ("marcá redes privadas y públicas").
  5. **Versiones distintas**: el magic `VM02` y el `VM1-` de la invitación no cambiaron con la pantalla compartida, así que 1.0.0 y la actual deberían entenderse. Confirmar igual qué versión tenía cada uno.
- [ ] **Diagnóstico (primer paso, pedirlo ya; sigue abierto)**: el log de esa sesión de los dos, `%AppData%\VoxMesh\log-AAAA-MM-DD.log`. **Se borra a las 48 h** (`logging.prune`). Del host: `UPnP UDP activo` o `ERROR UPnP UDP puerto …`, `IP pública obtenida` o `ERROR obtener IP pública`, `Invitación copiada (…)` (**la dirección que copió**: `setStatus` también va al log; la invitación con la clave no) y si aparece `Paquetes descartados` (llegaron paquetes con otra clave = invitación de otra sesión; si no aparece, no llegó nada = inalcanzable). Del que entra: `connectClient: conexión iniciada hacia …`.
- [x] **Arreglo mínimo del lado del que entra**: en vez del mensaje genérico, uno según la dirección de la invitación, con una función pura y su test. El caso principal es el de internet (con C7 casi todas las invitaciones van a ser públicas):
  - Pública: "El host no responde. Pedile que mire su registro de actividad: ahí dice si pudo abrir el puerto y qué hacer. Si la sala sigue abierta y el puerto está abierto, revisá que la invitación sea de esta sesión." Se apoya en los avisos de C6: el host ya tiene la respuesta en su pantalla.
  - Privada, CGNAT (`100.64.0.0/10`) o Radmin (`26.0.0.0/8`), solo si el host activó el check de C7: "Esta invitación es de LAN/VPN (IP): solo sirve en la misma red o VPN. Si no están en la misma, pedile al host la de Internet."
  - Opcional: al pegar una invitación LAN/VPN, aviso en naranja antes de conectar (sin bloquear).
  - **Hecho** (2026-11-12), la parte de código (el diagnóstico con los logs de arriba sigue abierto):
    - **El que entra**: `room.JoinFailedMsg(hostAddr)` ([failover.go](internal/room/failover.go)), pura, reemplaza a `JoinFailedKey`. Una dirección de internet da `room.join_failed_internet` ("El host no responde. Pedile que mire su registro de actividad…"); privada, CGNAT/Tailscale o Radmin da `room.join_failed_lan` con la IP. El clasificador es `netinfo.IsInternet`, el `isInternetAddr` de antes más `26.0.0.0/8`. Tests `TestJoinFailedMsg` y `TestIsInternet`.
    - **CGNAT (hipótesis 3)**: si UPnP anduvo, `checkRouterWAN` ([ui_connection.go](internal/ui/ui_connection.go)) le pide la IP externa al mismo cliente del router (`Mapping.ExternalIP`, `GetExternalIPAddressCtx`, 2 s), y `routerWANState` ([host_network.go](internal/ui/host_network.go)) la clasifica:
      - `100.64.0.0/10`: `hostNetCGNAT`, renglón rojo que no copia y `conn.net_cgnat` en el registro.
      - Privada: `hostNetDoubleNAT`, renglón naranja que **sí** copia. No es CGNAT sino un router detrás del módem del proveedor, y con DMZ o reenvío de puertos en el módem se llega: bloquear la copia rompía una sala que funcionaba. En el registro va `conn.net_double_nat`, con la IP del router para el módem y el puerto.
      - Pública pero distinta de la de ipify, o sin respuesta: queda listo, con una línea en el log.
      - Tests `TestRouterWANState` y `TestMappingExternalIP`.
    - **Hipótesis 4**: va en el README la línea del firewall ("marcá redes privadas y públicas"). La regla desde la app no se hizo.
    - **Quedó afuera**: el aviso opcional al pegar una invitación LAN/VPN; sin UPnP no hay a quién preguntarle la IP WAN, así que el CGNAT no se detecta; si falla ipify no se usa la IP del router como respaldo.
- **Hecho cuando**: se sabe la causa (o los logs descartan las anteriores) y el que entra ve un mensaje que le dice qué hacer.

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

### F5. Compartir pantalla para dar soporte

- [x] **Hecho (2026-11-03):** paquete [screen](internal/screen/) sin CGO: captura GDI ([capture_windows.go](internal/screen/capture_windows.go): `Monitors`, `Capturer` con `BitBlt` a una sección DIB + cursor con `DrawIconEx`, DPI por monitor en su propio hilo), tiles + `Diff` + `Scheduler` + `RefreshPolicy` ([tiles.go](internal/screen/tiles.go)), `Sender` ([sender.go](internal/screen/sender.go): gana la captura más nueva, sin colas), PNG ([encode.go](internal/screen/encode.go)), chunks ([packet.go](internal/screen/packet.go)), `Assembler` con topes ([assemble.go](internal/screen/assemble.go)) y filtro de área ([scale.go](internal/screen/scale.go)). Transporte: cola de pantalla propia de 1,5 Mbps sumando destinos (`SendScreen`, `TryRelayScreen`, `SetScreenRate`) y kinds 13–15 ([udp.go](internal/transport/udp.go)). Sala: `Participant.Sharing`/`Watching`, `SetSharing`, `SetWatching`, `Watchers`, `ScreenCopies` ([state.go](internal/room/state.go)). UI: [network_screen.go](internal/ui/network_screen.go), [screen_share.go](internal/ui/screen_share.go), [screen_view.go](internal/ui/screen_view.go), [ui_screen.go](internal/ui/ui_screen.go), insignia y "Ver pantalla" en el avatar, banner, `restoreWindow`. Detalle en AGENT.md §3.M. **Quedó afuera**: control remoto, audio de la PC, compartir una sola ventana, grabar, DXGI (lo protegido sale negro; el escritorio seguro de UAC no se ve).
- **Por qué**: dar soporte a un amigo (configurar algo viendo su escritorio). Importa que el texto se lea nítido; los fps no (1–2 por segundo alcanza).
- **Decisiones**:
  - Sin códec: tiles de 64×64 en PNG, solo los que cambiaron. Imagen completa al sumarse alguien a mirar, al cambiar el tamaño y 10 s después de que terminó la anterior. Si se pierde un chunk, ese tile queda viejo hasta entonces.
  - Hasta 1920 de ancho; más grande se achica con un filtro de área (no agranda nunca). ~~"Resolución completa" en el diálogo, apagada y sin guardar~~ → (2026-11-05) selector de resolución en el diálogo: Completa (solo si el monitor pasa de 1920) / 1080p (default) / 900p / 720p, guarda la última usada (`screen_resolution`). Ojo: achicar en una proporción no exacta (2560→1920, 1920→1600) pesa más que no achicar; solo ahorra la mitad justa.
  - Quién comparte y quién mira va en el estado de la sala, como el mute: varios pueden compartir, cada uno mira una pantalla, y **sigue con la migración de host** (probado).
  - Presupuesto 1,5 Mbps sumando destinos en el host; quien comparte desde un cliente lo divide por las copias que reenvía el host. Si nadie mira, no se captura ni se manda nada.
  - Captura con `CreateDIBSection` en vez de `GetDIBits`: en esta PC `GetDIBits` devolvía 0 líneas con un destino de más de ~1 MB.
- **Tests**: diff de tiles con bordes parciales y un píxel, `Scheduler` sin dejar tiles sin turno, cuándo va la imagen completa (5 casos), la captura nueva gana (los tiles de la vieja nunca salen y nada crece), chunks y rearmado (fuera de orden, chunk perdido, serial viejo, sesión nueva, topes, vencimiento), PNG sin pérdida, filtro de área (copia exacta, promedio exacto al 50 %, color plano, energía de una línea), quiénes miran en el host (alta, baja, desconexión, migración, copias), cola de pantalla (ritmo sumando destinos, `SetScreenRate`, descarta sin esperar, no espera detrás de la de archivos, Detener destraba), rutas del host, banner que no tapa los avatares, área del visor (minimizada). Contra la pantalla real: rectángulos en píxeles físicos, captura que no sale negra y tile que vuelve idéntico; `VOXMESH_SCREEN_BENCH=1` mide kbps.
- **Prueba real (2026-11-03, 2–3 instancias en esta PC por la IP de LAN, Windows al 100 %)**: compartir desde el host y desde un cliente; mirar desde dos a la vez; 1:1 idéntico píxel por píxel a la pantalla; Detener; matar al que comparte (a los 20 s se cierra el visor con "dejó de compartir"); el host se va mientras un cliente comparte y el visor sigue (el que compartía queda de host). Voz continua en nivel 2, antes y compartiendo: 0 % pérdida, 0 cortes, jitter ≤ 0,5 ms. Pantalla quieta ≈ 300–570 kbps por cada uno que mira; imagen completa ≈ 700 KB a 1080p. Se encontraron y arreglaron en la prueba: el banner tapaba los avatares, el visor minimizado volvía a abrirse minimizado y sin imagen, y los tiles llenaban el log de nivel 2.
- [x] **Segunda vuelta (2026-11-04): "es lento inclusive local".** En la prueba del 2026-11-03 43 de 49 capturas quedaron atrasadas: el tope de 1,5 Mbps hacía tardar ~4 s la imagen completa. Cambios (detalle en AGENT.md §3.M):
  - **Ritmo de LAN**: el host detecta LAN (IP privada + el ping más bajo de los últimos 10 s ≤ 10 ms, [link.go](internal/screen/link.go)) y usa 20 Mbps; si alguien está por internet, 1,5 Mbps para todos. Le dice a quien comparte desde un cliente cuánto puede mandar (`PacketScreenRate`, 16). La ráfaga de la cola crece con la tasa.
  - **Menos latencia**: captura cada 150 ms mientras la pantalla cambia y 500 ms quieta ([cadence.go](internal/screen/cadence.go)); el visor pinta cuando termina una tanda (no cada 250 ms) y achica solo los tiles que cambiaron (`Scaler.ScaleRect`).
  - **Menos bytes**: tiles lisos de 4 bytes y PNG con paleta si hay ≤ 256 colores ([encode.go](internal/screen/encode.go)): ~28 % menos y el doble de rápido.
  - **Reparación en vez de la imagen completa cada 10 s**: resumen cada 2 s (`PacketScreenDigest`, 18) y quien mira pide lo que le falta (`PacketScreenRepair`, 19) ([digest.go](internal/screen/digest.go)).
  - **Cursor aparte** (cambia el pedido original de dibujarlo en la imagen): posición hasta 20/s sin cola (`PacketScreenCursor`, 17) y forma una vez como tile especial ([cursor.go](internal/screen/cursor.go), [cursor_windows.go](internal/screen/cursor_windows.go)); el visor lo dibuja en una capa encima.
  - **Prueba real (2026-11-04, 3 instancias, cliente que comparte → host → cliente que mira)**: ritmo de LAN detectado; primera imagen completa en 318 ms (antes ~4 s); pantalla quieta ≈ 95 kbps (antes ≈ 290); con el visor visible (espejo) y el cursor moviéndose ≈ 270 kbps; 448 capturas y **0 atrasadas** (antes 43 de 49); cursor dibujado en el lugar correcto; voz continua igual que sin compartir (0 % pérdida, 0 cortes, jitter ≤ 0,3 ms).
- **Hecho cuando**: falta la prueba manual de abajo (otra PC, internet, escalado de Windows).

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
- [x] **R6. Dos registros con la misma dirección**: si alguien cambia de nombre y reconecta desde el mismo puerto, el host tiene dos IDs con la misma dirección. `handleHostPacket` actualiza `LastSeenUTC` del **primero** que encuentra (el viejo), así que el nuevo vence a los 20 s; `handleParticipantLeave` busca por ID **o** dirección, da con el viejo (ya desconectado) y vuelve sin marcar nada, pero antes hizo `RemovePeer`: el cliente pierde la difusión hasta su próximo paquete, cada 2 s. Arreglo probable: en `handleRoomHello`, otro ID en la misma dirección es el mismo socket → sacarlo de la lista.
  - **Hecho** (2026-11-13, con C2): `State.Supersede(id, address)` ([state.go](internal/room/state.go), pura): otro ID desde la misma "IP:puerto" sale de la lista, conectado o no; nunca el host ni el propio. Al que miraba su pantalla se le borra el `Watching`. Reemplaza el slice, como `SetMuted`. `handleRoomHello` ([network.go](internal/ui/network.go)) la llama antes de `UniqueUsername` (así el viejo no le ocupa el nombre). El que entra hereda el `Order` del que reemplaza (el orden de la sala decide la migración) sin pasar por `MakeRoom`, y `removePeerAddress` saca la dirección vieja salvo que sea la suya. Una línea de log ("Vuelve X: reemplaza su registro anterior…"). No sale "X salió" del viejo; "X entró", como en cualquier reconexión. Tests `TestSupersedeSameAddress` y `TestRoomHelloSameAddressReplacesOldRecord`. **Quedó afuera**: `handleHostPacket` y `handleParticipantLeave` siguen buscando por dirección sin mirar si está conectado; con un solo registro por dirección ya no hace falta.
- [x] **C2. "Nombre (2)" al reconectar desde otro puerto**: el registro viejo sigue "conectado" hasta `participantTimeout` (20 s), así que el que vuelve en menos de eso con un ID nuevo entra como "Nombre (2)".
  - **Hecho** (2026-11-13): `State.Supersede(id, name, address, now, silence)` ([state.go](internal/room/state.go)) suma a la regla de R6 la de C2: el mismo nombre (sin mayúsculas) desde la misma IP y otro puerto (`sameIP`, con `netip`: una IPv4 dentro de IPv6 cuenta como IPv4), callado hace más de `room.ReturnSilence` (**5 s**, decidido con Coffee al ver el plan), sale de la lista, conectado o no. Un cliente vivo le manda al host un ping y un pong cada 2 s: si el registro viejo sigue mandando, es otra persona detrás del mismo router con el mismo nombre (dos "Usuario" en la misma casa) y le toca " (2)", como antes. `handleRoomHello` la llama con el nombre ya recortado; el que entra hereda el lugar (`Order`) del viejo, como en R6. Verificado al hacer el plan: el ID del cliente es un hash del nombre y de la dirección **local** (`[::]:47830`), así que con el puerto por defecto no cambia al reconectar y no había "(2)"; cambia cuando el 47830 está ocupado (dos instancias en la misma PC, otro programa) y se cae a `:0`. Tests `TestSupersedeSameNameAndIP` (callado, mayúsculas, IPv4 en IPv6, desconectado, vivo, otra IP, otro nombre, dirección rota, el host nunca) y `TestRoomHelloSameNameAndIPKeepsTheName` (callado → "Pepe" con su lugar; vivo → "Pepe (2)"). DESARROLLO ("Nombres repetidos") y AGENT.md §3.E. **Quedó afuera**: una red nueva (otra IP pública, por ejemplo pasar del Wi-Fi a los datos) sigue entrando como "Nombre (2)" hasta que el registro viejo vence a los 20 s.
  - **Decidido con Coffee (2026-11-13), va en la fila 4 con C18**: mismo nombre y misma IP (otro puerto) en la misma sesión de la sala es la misma persona. El host saca el registro viejo y no le pone "(2)". Con otra IP sigue como hoy: puede ser otra persona con el mismo nombre.
  - Va en `handleRoomHello`, igual que R6 (misma dirección, otro nombre). Hacer R6 de paso si sale con el mismo arreglo.
- [ ] **R2. Chat / miniaturas**, si el chat se pone lento con muchas imágenes: `refreshChat` reconstruye todas las filas en el hilo de UI (en cada tecla del buscador y por cada imagen recibida) y lanza una decodificación por imagen sin deduplicar. Arreglo: juntar los refresh (flag sucio + un solo `fyne.Do`), cargas deduplicadas + semáforo de 1–2 decodificaciones, `thumbCache` con tope; `widget.List` virtualizado solo si hace falta.
- [x] **C4. Ajustes pide ~1000 px de ancho** (visto en C3, ya pasaba antes): con la ventana a media pantalla (~960 px), abrir Ajustes la agranda a ~1000 px y la tarjeta de la derecha y "Guardar" quedan cortadas. Sospechoso: `container.NewGridWithColumns(3, …)` de la tarjeta "Interfaz y Sistema" ([ui_settings.go](internal/ui/ui_settings.go)), donde cada columna mide lo que "Abrir carpeta de datos y grabaciones". Arreglo probable: esos tres botones en dos filas o en un VBox.
  - **Hecho** (2026-11-02): además de esos tres botones (ahora uno debajo del otro), lo ensanchaban los textos de los medidores, que cambian solos ("Nivel: … · SATURA (bajá el volumen del micrófono)", "Después de filtros: …", "Voz detectada (VAD): …") y en la grilla Entrada | Salida contaban doble, y el "Etiqueta: valor" de cada slider de "Configuración avanzada" ([ui_audio_params.go](internal/ui/ui_audio_params.go)). Ahora hacen salto de línea: cuando aparece "SATURA", el texto baja una línea en vez de ensanchar. La regla quedó en AGENT.md §3.D.
- [ ] **R7. Ctrl+V de imágenes trabaja en el hilo de UI**, solo si se nota: `tryPasteImage` ([ui_chat.go](internal/ui/ui_chat.go)) corre en `TypedShortcut` (callback de Fyne, goroutine 1). Ahí `readClipboardImage` ([file_windows.go](internal/ui/file_windows.go)) hace `os.ReadFile` cuando se copió un archivo en el explorador (sin tope de tamaño) y, con una captura (`CF_DIB`), `bmp.Decode` + `png.Encode`. Con una captura grande la ventana se puede trabar unos cientos de ms, y AGENT.md §3.G lo prohíbe. Arreglo probable: en el hilo de UI solo abrir el portapapeles y copiar los bytes o la ruta (`OpenClipboard`/`CloseClipboard` tienen que correr en el mismo hilo); leer el archivo y convertir a PNG en una goroutine que termina con `stageImage` vía `fyne.Do`.
- [ ] **C24. Los atajos de una tecla no suenan si en VoxMesh quedó el foco en una caja de texto** (visto en C21, 2026-11-14): escribís en el chat, pasás al juego con Alt+Tab y un atajo sin Ctrl/Alt/Shift (una letra o un número) no suena. `isTypingInApp` ([hotkey_windows.go](internal/ui/hotkey_windows.go)) solo mira `Canvas().Focused()`, y Fyne 2.6.3 conserva ese foco aunque la ventana pierda el del sistema. Al ocultar la ventana en el área de notificaciones (C21) ya se suelta (`Unfocus`). Arreglo probable: además, que la ventana de VoxMesh sea la de adelante (`GetForegroundWindow` == su HWND, leído en el hilo de UI). Unas 3 líneas.
- [ ] **R10. Apagar Windows con VoxMesh abierto no cierra la sala** (visto en C21, 2026-11-14): Windows no manda WM_CLOSE al apagar y GLFW ignora WM_QUERYENDSESSION/WM_ENDSESSION, así que el proceso muere sin `quit`: sin Leave (los demás lo ven salir a los 20 s; si era host, la sala migra por silencio), la regla UPnP queda hasta que vence (≤ 8 h; la próxima sala la reusa) y se pierde solo el tamaño de la ventana (la config se guarda en cada cambio). Pasaba antes de la bandeja; con la X que oculta es más probable apagar estando en una sala. Arreglo probable: una ventana oculta de nivel superior (no message-only) en su propio hilo bloqueado, que en WM_ENDSESSION cierre la sala y la config dentro de ~3 s antes de volver. Se prueba recién apagando la PC.
- [ ] **C25. El atajo "Numpad +" nunca anda** (visto en C20, 2026-11-14): el diálogo lo detecta y lo guarda ("Numpad +", o "Ctrl + Numpad +"), pero `parseHotkeyString` ([hotkey_windows.go](internal/ui/hotkey_windows.go)) corta el texto en cada "+" y no encuentra la tecla: el atajo queda mostrado y nunca suena (tampoco cuenta para la tecla repetida). Arreglo probable: separar por " + " (con espacios, como lo arma el diálogo) o cortar el nombre de la tecla al final, y un test con "Numpad +".
- [ ] **T3. `TestBannerResolutionChangesLive` falla a veces al borrar su carpeta temporal** (visto en la fila 5, 2026-11-13; 1 de 4 corridas, sola pasa 10 de 10): "TempDir RemoveAll cleanup: … El directorio no está vacío". Elegir una resolución llama a `saveConfig`, que escribe `voxmesh.json` en otra goroutine (`configSaver`); si el test termina antes, la escritura y el borrado se cruzan. Es del test, no de la app. Arreglo probable: que el test espere el guardado (por ejemplo, un `flushConfig` y que `configSaver` termine) o que use una config que no se guarde en la carpeta temporal. Los demás tests que guardan la config al final pueden tener lo mismo.
- [ ] **T4. `TestEchoReference_Concurrent` falla a veces con `-race`** (visto en la fila 7, 2026-11-14; 1 de 8 corridas del paquete `audio`, sola pasa 20 de 20; el código es de la 1.0.0 y la fila 7 no lo tocó): el escritor del test (`writeRef`, 20 000 callbacks seguidos, sin pausa) le saca más de un ring (65 536 muestras) de ventaja al lector, y `echoReference.put` pisa el lugar que `echoReader.take` está copiando. Es el caso "el lector se atrasa más que el ring, el escritor nunca espera" del diseño (AGENT.md §3.A 1b): en la app, solo si la goroutine de envío se traba más de ~1,36 s, y lo leído se descarta como referencia vieja. Decidir: pausar el escritor del test para que no le saque un ring de ventaja (lo que se prueba es el orden y el salto, no la carrera), o que `take` vuelva a mirar `written` después de copiar y descarte lo que se pisó. Mientras tanto, si falla solo eso, repetir.
- [ ] **T2. `TestBindTooltip` falla con `-race`** (visto en la fila 4, 2026-11-13; ya fallaba en `3827311`, antes de tocar nada): el driver de test de Fyne corre el `fyne.Do` del timer de la leyenda (`bindButton.MouseIn` → `tooltip.show` → `Container.Show`) en otra goroutine mientras el test lee `Visible()` y mueve el mouse. Es del test, no de la app (en la app el `fyne.Do` corre en el hilo de UI). Sin `-race` pasa. Arreglo probable: que el test espere la leyenda leyendo el estado dentro de un `fyne.DoAndWait`, o no usar el timer real (inyectar el retardo). Mientras tanto: `go test -race -skip TestBindTooltip ./internal/ui/`.

## Pruebas manuales pendientes

Las anteriores se hicieron todas el 2026-11-02 y salieron bien (resumen en "Historial").

- [ ] **C15. Paletas de colores** (2026-11-14, fila 8): en Ajustes → Interfaz y Sistema elegir Naranja y Clara: toda la ventana cambia al instante, sin reiniciar (tarjetas, botones, chips, medidores, el banner de compartir, la leyenda de los atajos, el chat con una búsqueda). Con Clara, que se lea todo (chips, medidores de Ajustes, insignias de mute y pantalla, el registro). "Descartar" vuelve a la de antes; "Guardar" la deja al reiniciar. "Editar colores…": cambiar el resaltado y el fondo, "Aplicar" (se ven), "Cerrar" sin guardar (vuelve la de antes); otra vez y "Guardar como…" "Mi paleta": aparece en la lista, se puede renombrar y borrar (vuelve Default). Default tiene que verse igual que la versión anterior. Elegir cuál queda por defecto.
- [ ] **F8 + C19 + C22 + C23** (2026-11-14, fila 7, con el exe nuevo y dos PCs o dos instancias):
  - **F8**: que alguien grite de verdad (y hable normal) con "Bajar los gritos que llegan" prendido y apagado (Ajustes → Salida → Configuración avanzada → Gritos que llegan), con su volumen individual al 100 % y al 300 %. Prendido, el grito tiene que llegar ~10 dB más bajo y la voz normal igual que apagado, sin "bombeo" (que suba y baje entre sílabas) ni clics. Si toca voces normales fuertes, subir "Desde qué volumen" (−12, −10); si un grito pasa, bajarlo. Con "Cuánto" en −20 se tiene que notar más. Anotar los valores que queden mejor para cambiar los de fábrica.
  - **C19**: en Entrada y en Salida elegir "Sin filtros": todos los checks apagados (salvo el general), la ayuda debajo de la barra, Renombrar y Borrar grises. Prender un filtro y "Guardar" → pide un nombre; tocar el Guardar de Ajustes → crea "Personalizado". Cerrar y abrir la app con "Sin filtros" activo: sigue activo y con sus valores. En inglés se llama "No filters". Intentar guardar un perfil llamado "sin filtros" o "No filters": lo rechaza.
  - **C22**: en una sala, hablar bajo y fuerte mientras se mueve el slider debajo de tu nombre: la barrita verde cuando se manda (el aro del avatar de los demás lo confirma), gris cuando no y muteado. Con Default activo, al soltarlo aparece "Se creó el perfil «Personalizado»…" en la barra de estado; reiniciar la app y que el valor siga. Con "Sin filtros", moverlo prende la puerta. Abrir Ajustes, cambiar "Volumen mínimo para mandar", guardar (y otra vez descartando): el slider del chip queda igual que Ajustes. La leyenda al pasar el mouse.
  - **C23**: el slider de cada amigo y el de la botonera: la mitad del recorrido tiene que sonar como "la mitad", el 100 % se encuentra solo (el clic) y un volumen guardado antes (por ejemplo 50 %) sigue sonando igual. La botonera en 0: un sonido propio no suena y "Bloquear sonidos ajenos" se prende; al subirla se apaga.

- [ ] **R8. Mensajes al no poder entrar y CGNAT** (2026-11-12, con el exe nuevo):
  - Entrar con una invitación de una sala ya cerrada (o de otra sesión) por internet: a los 10 s, "El host no responde. Pedile que mire su registro de actividad…". Con una de LAN/VPN que no llega (otra red, o Radmin apagado): "Esta invitación es de LAN/VPN (IP)…", con la IP. Las dos, en inglés también.
  - Host detrás de CGNAT (datos móviles compartidos desde el celular, o un proveedor con CGNAT) con UPnP en el router: renglón rojo "Internet: tu proveedor no deja entrar (CGNAT)" + el gris; el registro dice que cree la sala otro o usen LAN/VPN; el log tiene `IP externa del router: 100.x…`.
  - Router detrás del módem del proveedor (doble NAT): "Internet · IP:puerto (router detrás de otro)" en naranja, copia con el aviso del módem; abriendo el puerto en el módem (o DMZ) entran.
  - Router normal: nada cambia ("Sala lista por internet"), y si el router no contesta la IP externa, el log lo dice y la sala queda lista igual.
- [ ] **C6 + C7. "Invitar" con el estado de la red del host** (2026-11-12, con el exe nuevo y el check de LAN/VPN apagado salvo que se diga):
  - Iniciar sala y tocar "Invitar" enseguida: el popup dice "Obteniendo IP pública…" en naranja y no copia nada; el botón tiene un reloj naranja que se va solo. Al terminar, el registro de actividad tiene **solo dos líneas** ("Obteniendo IP pública y abriendo el puerto…" y "Sala lista por internet: ya podés invitar.") y "Invitar" copia directo, sin popup. La otra instancia (cliente) no ve esas líneas.
  - Router sin UPnP o con UPnP apagado: "Internet · IP:puerto (puerto sin abrir)" en naranja y el renglón gris de LAN/VPN abajo. Al copiar, la barra avisa del puerto; el registro dice qué hacer, con el puerto UDP y la IP local.
  - Sin internet (cable o Wi-Fi desconectado antes de iniciar la sala): renglón rojo "Internet: no se pudo obtener la IP pública" + el gris; el registro dice "No se pudo obtener la IP pública…".
  - Migración: el que queda de host ve el reloj, sus dos líneas y nunca la IP del host viejo como "Internet".
  - Cerrar la sala mientras todavía busca (antes de 15 s): no aparece la línea del resultado y el botón vuelve al ícono de copiar.
  - Apagado: no aparece ninguna 192.168.x ni 26.x en el popup. Prendido: aparecen debajo de Internet (también mientras busca), y la opción queda guardada al reabrir la app. "Descartar" en Ajustes la vuelve atrás.
  - El popup: los renglones que copian se resaltan con el mouse, un clic afuera lo cierra, no se sale de la ventana por la derecha, y se ve bien en inglés y en español.

- [ ] **C8. "Invitar" con todas las direcciones** (2026-11-13, con el exe nuevo):
  - Instalación nueva (o sin `show_lan_invites` en `voxmesh.json`): el check de Ajustes → Interfaz y Sistema está prendido. Quien ya lo había guardado apagado lo sigue viendo apagado.
  - Iniciar sala y tocar "Invitar" enseguida: arriba, en naranja, "Abriendo el puerto y obteniendo la IP pública…" (lo mismo que la primera línea del registro); abajo las LAN/VPN con "(misma red o VPN)". Al terminar, Internet arriba y las LAN/VPN abajo; ya no copia directo: siempre abre el popup.
  - Router sin UPnP (o UPnP apagado): renglón rojo "Error al abrir el puerto: mirá el registro de actividad" y abajo "Internet · IP:puerto (puerto sin abrir)" en naranja. Con la columna izquierda oculta, tocar el rojo cierra el popup y la muestra con el registro; el naranja copia con el aviso del puerto.
  - Sin internet y CGNAT: tocar el renglón rojo también muestra el registro.
  - Apagando el check: como antes (sin IPs locales y el renglón gris tras una falla). Todo en inglés también.

- [ ] **C9. Duración del puerto (UPnP)** (2026-11-13, con el exe nuevo y un router con UPnP):
  - Ajustes → Red y Conectividad: "Tiempo que queda abierto el puerto (UPnP): 8 h" con la ayuda; el slider va de 1 a 24; "Descartar" lo vuelve atrás y "Guardar" lo deja al reabrir la app.
  - Con 1 h: iniciar sala → la línea del resultado dice "Sala lista por internet: ya podés invitar. El puerto queda abierto hasta las HH:MM (1 h)." con la hora de ahora + 1 h (en la barra de estado también); el log dice `UPnP UDP activo: … por 1h0m0s`. Iniciando cerca de la medianoche dice "hasta mañana a las…".
  - Dejar la sala abierta esa hora: a lo sumo un minuto después del vencimiento aparece **una** línea "Se cumplió el tiempo del puerto abierto (1 h)…" (y el log `venció el puerto externo`); comprobar si alguien de afuera todavía puede entrar (depende del router).
  - Cerrar la sala o pasar a cliente antes de que venza: no aparece la línea. Al quedar de host por migración, el que queda usa su propia duración.
  - Sin UPnP, sin IP o con CGNAT: la línea del resultado no dice nada del puerto y nunca aparece la de vencido. Todo en inglés también.

- [ ] **C10. "Acerca de"** (2026-11-13): con un exe de `build.ps1`, Ajustes → Interfaz y Sistema → "Acerca de VoxMesh" muestra ícono, "Versión <tag>-dev.<fecha> · commit <hash corto>" (el de `git log -1`), "Hecho por DrCoffee84" (el link abre el repo en el navegador), "Licencia MIT"; "Buscar actualizaciones" muestra el resultado encima y "Cerrar" lo cierra. Tocar la versión abajo a la derecha abre lo mismo. Con un `go build` sin `-X`: "commit dev". En inglés también; el diálogo no ensancha la ventana a ~960 px.

- [ ] **C11. Nombre al abrir la app** (2026-11-13):
  - Instalación nueva (sin `voxmesh.json`, en `%AppData%\VoxMesh`): al abrir sale "What's your name?" (en inglés, el idioma de fábrica) con la entrada lista para escribir; Guardar deshabilitado hasta escribir algo; Enter guarda; el nombre aparece arriba y queda al reabrir; no vuelve a preguntar.
  - Con "Usuario" (o "Usuario (2)") guardado: "Ahora no" y Esc cierran sin cambiar nada y la próxima vez vuelve a preguntar; con "No volver a preguntar" marcado, no pregunta más (y `voxmesh.json` tiene `"username_prompt_off": true`).
  - Con un exe de release y una versión nueva publicada: primero el nombre y, recién al cerrarlo, el aviso de actualización (nunca encima). Con un nombre propio, el aviso sale como antes.
  - Maximizada al abrir: el diálogo queda centrado. En español también.

- [ ] **C12. Cruz y Esc en la imagen ampliada** (2026-11-13): abrir una imagen del chat → la ✕ arriba a la derecha se ve (también sobre una imagen clara y en una muy ancha o muy alta) y cierra; Esc cierra, también si antes estabas escribiendo en el chat (al cerrar, el cursor sigue en el chat); tocar la imagen o afuera sigue cerrando; abrir y cerrar seguido varias veces no traba la ventana.

- [ ] **F6. "Detener para todos"** (2026-11-13), con dos instancias (host y cliente) y un sonido largo:
  - El que lo mandó toca "Detener para todos": se corta en las dos. Lo mismo tocándolo desde la otra instancia (la que no lo mandó), como host y como cliente.
  - "Detener" (y su atajo) sigue cortando solo en la PC propia; la vista previa del recorte ("⏹ Detener") no corta a nadie más.
  - Un sonido que la otra instancia todavía no tiene (recién agregado y borrado de su carpeta, o uno largo por internet): tocarlo y enseguida "Detener para todos" mientras se baja → no arranca cuando termina de llegar; tocarlo de nuevo después suena desde el disco.
  - El botón se ve entero en inglés y en español.

- [ ] **F7. Ensordecer** (2026-11-13), con dos instancias en sala y un sonido largo de la botonera:
  - Sin mute: tocar los auriculares → se tachan en rojo, el mic también; dejás de escuchar al otro sin clic ni ruido; el otro ve tu insignia de mute. Volver → auriculares y mic normales, se escucha enseguida (sin audio viejo acumulado ni retraso) y el otro te escucha.
  - Con mute previo: ensordecer y volver → el mic sigue muteado.
  - Ensordecido, tocar el mic → vuelven el sonido y el micrófono juntos.
  - Ensordecido: un sonido que estaba sonando se corta; los que tira el otro no suenan (tampoco después, al volver, uno que se estaba bajando); el que tocás vos no suena acá pero el otro lo escucha. Los sonidos de aviso (alguien entra, un mensaje) sí suenan. Mientras habla el otro, su avatar sigue marcando que habla.
  - Con "Escuchar mi voz en vivo" prendido: ensordecido no te escuchás.
  - "Reiniciar audio" y una migración de host estando ensordecido: sigue ensordecido.

- [ ] **R6 + C2. Volver a entrar sin "Nombre (2)"** (2026-11-13), host en una PC y un cliente en otra (misma red o internet):
  - El cliente cierra la app de golpe (Administrador de tareas) y vuelve a entrar enseguida, con el puerto 47830 ocupado (por ejemplo, otra instancia abierta en esa PC, que así escucha en otro puerto): entra con su nombre, sin "(2)", en el mismo lugar de la lista; no aparece "X salió" a los 20 s; el log del host dice "Vuelve X: reemplaza su registro anterior…".
  - Dos instancias en la misma PC con el **mismo** nombre y las dos vivas: la segunda entra como "Nombre (2)" y la primera no desaparece de la lista.
  - Cambiarse el nombre en Ajustes y volver a entrar desde el mismo puerto (R6): queda un solo registro, con el nombre nuevo, y no se cae a los 20 s.
- [ ] **C18. Gráfico de lag** (2026-11-13), un host y dos clientes (uno idealmente por Wi-Fi):
  - Al abrir la app el ícono del gráfico (a la derecha de "En sala") está gris y no hay tiras; con el mouse encima dice "Mostrar detalle de lag". Tocarlo → aparecen las tiras debajo de cada uno (no en el propio) y el ícono queda en color; "Ocultar detalle de lag". Cerrar y abrir la app: sigue como quedó y el historial arranca vacío.
  - Con el mouse sobre la tira: "21:34:12 · 35 ms · 0 % de pérdida". Tocarla → el gráfico grande, con "Toda la sesión", 5 min y 1 h; el renglón de abajo muestra el momento bajo el mouse; "Cerrar" lo cierra.
  - Cortar el Wi-Fi de un cliente unos segundos: en el host y en el otro cliente, su tira y su gráfico muestran el hueco (o el pico) en el momento justo; al volver, sigue en el mismo gráfico (también si reconecta).
  - En un cliente: el chip del host dice sus ms (también si entró por una dirección LAN/VPN) y el de los demás "≈NN ms"; el gráfico de alguien que no es el host avisa que desde ahí solo se mide la pérdida.
  - Salir y volver a entrar a la sala, y una migración de host: los gráficos siguen con lo anterior.
- [ ] **C13. Lag largo en el registro** (2026-11-13), con un cliente al que se le pueda empeorar la red (Wi-Fi lejos del router, una descarga grande, o `clumsy` en Windows con 300 ms de demora):
  - Más de 10 s con lag: una sola línea "Hay lag con X desde las HH:MM (… ms, … % de pérdida)." con la ayuda; al volver a lo normal 10 s, otra "Se normalizó la conexión con X (duró …; lo peor: …)." En el cliente, del host: "con el host (X)".
  - Un pico de pocos segundos no avisa nada; lag que va y viene no llena el registro (como mucho un aviso por persona cada 5 min).
  - En el gráfico grande, el tramo queda con la franja roja arriba. Todo en inglés también.

- [ ] **C14. Problemas en el registro de actividad** (2026-11-13, con el exe nuevo). Cada línea dice qué hacer, la barra de estado muestra el texto corto y el mismo problema no se repite antes de 5 min:
  - **Dispositivo**: elegir unos auriculares USB en Ajustes, desconectarlos y entrar a una sala (o tocar "Reiniciar audio"): "No encontré el micrófono «…»: uso el predeterminado de Windows. Si lo querés usar…".
  - **Audio caído**: en una sala, desconectar el USB o Bluetooth que se está usando: "Se cayó el audio (…), reabriendo..." con la ayuda y, al reabrir, "Audio listo: micrófono «…», salida «…»". Si no hay ningún dispositivo: "No se pudo abrir el audio: tocá Reiniciar audio." con la ayuda.
  - **Celular**: en una sala con el celular de micrófono, bloquearlo: "Celular desconectado del micrófono." con la ayuda. Al desbloquearlo, "Celular conectado como micrófono.". Elegir otro micrófono en Ajustes no avisa nada.
  - **Micrófono que pierde audio**: con un micrófono Bluetooth (o lejos de la PC), hablar un rato: si el log tiene `Envío: … (4x.x/s)` dos veces seguidas, aparece "Tu micrófono está perdiendo audio…". Muteado no avisa.
  - **Invitación vieja**: iniciar sala, copiar la invitación, volver a iniciar la sala y entrar desde otra PC con la invitación vieja: en el host, "Alguien intenta entrar con una invitación vieja o de otra sala…". El que entra ve "El host no responde. Pedile que mire su registro de actividad (☰)…".
  - **Puerto ocupado**: abrir dos VoxMesh en la misma PC e "Iniciar sala" en las dos: la segunda dice "No se pudo abrir el puerto …" + "¿Hay otra copia de VoxMesh abierta?…".
  - **Invitación inválida**: pegar algo que no es una invitación y tocar Conectar: "Invitación inválida: …" + "Pedí la invitación de nuevo…".
  - El ☰ se ve bien en "Invitar" (renglón rojo) y en la leyenda del gráfico de lag. Todo en inglés también.

- [ ] **C20. Prender y apagar los atajos de la botonera** (2026-11-14, con el exe nuevo y un par de sonidos con atajo):
  - Debajo de "Detener para todos": "Atajos: sí" con el teclado y "Bind" al lado. Tocarlo → "Atajos: no" con el teclado en rojo, suena un tono que baja (también con "Sonidos de eventos" apagado, en sala y fuera) y el registro dice "Atajos de la botonera apagados."; los atajos de los sonidos y el de "Detener" no suenan, pero los botones sí. Tocarlo de nuevo → tono que sube y vuelven todos.
  - Asignarle un atajo (por ejemplo F8) y, en el Bloc de notas o un juego: F8 apaga y prende (con el aviso), y el botón de VoxMesh cambia solo. Cerrar y abrir la app apagados → siguen apagados.
  - Elegir para un sonido la tecla de "Detener" (o la de otro sonido, o la de los atajos): el diálogo dice "… ya lo usa «…». Elegí otra tecla." y no la toma; otra tecla sí. Volver a aceptar la que ya tenía, sin cambiarla, la deja.
  - Ensordecido: los avisos suenan igual. Se ve entero en inglés y en español.

- [ ] **C21. La X al área de notificaciones** (2026-11-14, con el exe nuevo, idealmente con un amigo en la sala):
  - Al abrir, el ícono de VoxMesh aparece junto al reloj; con el mouse encima dice "VoxMesh". Clic izquierdo y derecho abren el menú: Abrir VoxMesh, Colgar (gris fuera de una sala), Silenciar micrófono, Ensordecer, Atajos de la botonera (con tilde) y Salir; sin un "Quit" de más.
  - La X la primera vez: el aviso "VoxMesh sigue abierto". "Entendido" la oculta; en una sala, el otro te sigue escuchando y vos a él. "Abrir VoxMesh" la vuelve a mostrar adelante, también si estaba maximizada (y vuelve maximizada). Con "No volver a mostrar", la próxima X oculta directo. "Salir de VoxMesh" del aviso, en una sala: el otro ve que te fuiste enseguida (no a los 20 s).
  - Desde el menú, con la ventana oculta: Silenciar micrófono y Ensordecer hacen lo mismo que los botones (el otro ve la insignia de mute) y la tilde sigue a lo que se cambia en la ventana; Atajos de la botonera los apaga y prende con el tono; Colgar sale de la sala.
  - "Salir" del menú en una sala: sale sin preguntar y el otro ve que te fuiste enseguida; como host, la regla del router se borra (el log dice que se quitó el mapeo UPnP).
  - Ajustes → Interfaz y Sistema: apagar "Al cerrar la ventana, seguir abierto junto al reloj" → la X cierra como antes (en sala pregunta). Prenderla de nuevo y "Descartar" la deja como estaba.
  - Con la ventana oculta, abrir de nuevo el exe (doble clic): aparece la ventana que estaba, adelante, y no queda un segundo VoxMesh en el Administrador de tareas. Una copia del exe en otra carpeta sí abre aparte.
  - Escribir en el chat, cerrar con la X y apretar un atajo de una tecla (una letra) en el Bloc de notas: suena.
  - Actualizar con la ventana oculta (una release nueva; "Buscar actualizaciones" y ocultarla mientras baja): la versión nueva abre normal, sin ceder el lugar a la vieja.
  - Abrir el menú del ícono y dejarlo abierto unos segundos: el log no dice "hilo de UI bloqueado". Todo en inglés también.

- [ ] **F5. Pantalla compartida**, lo que no se pudo probar en una sola PC (incluye la segunda vuelta del 2026-11-04: por internet el log tiene que decir "ritmo de internet"; el cursor tiene que verse fluido y en el lugar justo, también el de texto, que va negro con borde blanco):
  - Windows al **125 % y 150 %** (Configuración → Pantalla → Escala) en quien comparte y en quien mira: la captura sale entera y del tamaño físico (`go test -run TestMonitorsArePhysicalPixels -v ./internal/screen/` con ese escalado) y en "Tamaño real (1:1)" la letra chica se lee igual que en la pantalla original.
  - **Dos PCs** (LAN y por internet): compartir, mirar, 1:1, Detener, cerrar la app del que comparte. Con voz al mismo tiempo y log en nivel 2 (Avanzado), comparar las líneas `Audio de …` (pérdida, jitter, cortes) antes y mientras se comparte, también en la PC del host, que reenvía; mirar `Resumen pantalla` y `Resumen cola de pantalla` (kbps reales) con la pantalla quieta y con mucho movimiento (un video o scrollear una página).
  - **Dos monitores**: el selector aparece, por defecto el principal, y compartir el otro funciona. Un monitor de 1440p o 4K con cada opción del selector de resolución; "Completa" aparece solo al elegir ese monitor.
  - (2026-11-05) **Resolución en vivo y aviso en la barra**: cambiar la resolución desde el banner mientras alguien mira (la imagen se rearma con el tamaño nuevo, en "ajustar" y en 1:1, con el cursor en su lugar; el log dice `Pantalla: resolución: …`); el punto rojo sobre el botón de VoxMesh en la barra de tareas y el título "VoxMesh · Compartiendo" aparecen al compartir (también minimizada) y se van al detener, al desconectarse y al cerrar; la voz igual que antes.
  - Que el cursor no parpadee en la PC del que comparte (`CAPTUREBLT`; si parpadea, sacarlo en `capture_windows.go`) y qué se ve con un cartel de UAC abierto (debería quedar la última imagen).

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

### R9. Optimizar RAM y CPU

- [ ] **Idea (Coffee, 2026-11-13)**: optimizar de verdad, sin trucos (nada de achicar a la fuerza el número del Administrador de tareas). Se piensa más adelante. Hoy usa entre 130 y 200 MB en una PC floja, y no es urgente.
- **Puntas vistas el 2026-11-13**:
  - Medir primero: Go (heap y sys) contra la memoria del proceso.
  - `thumbCache` no tiene tope (ver R2).
  - `memoryLimit` (512 MB) nunca actúa.

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
