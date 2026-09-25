# VoxMesh

VoxMesh es un chat de voz + texto host-cliente en Go para Windows, organizado en **salas** con failover automático de host. Usa UDP, una puerta de ruido configurable y una interfaz Fyne (`v2.6.3`) en un único ejecutable.

## Stack técnico

- **Lenguaje:** Go 1.26.
- **GUI:** `fyne.io/fyne/v2` `v2.6.3` (requiere CGO + GLFW/OpenGL en Windows).
- **Transporte:** UDP puro (`net.UDPConn`), sin TCP. Paquetes propios con cabecera binaria (magic `VM01`, 16 bytes: kind, sequence, payload length).
- **Audio:** `github.com/gen2brain/malgo` (miniaudio) para captura/reproducción, `github.com/VoiceBlender/rnnoise-go` para supresión de ruido + VAD, Noise Gate por RMS propio, PCM mono 48 kHz.
- **Red/NAT:** `github.com/huin/goupnp` para mapeo UPnP automático de una hora; `api.ipify.org` para IP pública.
- **Diálogo de archivos:** nativo de Windows vía `comdlg32.dll` / `GetOpenFileNameW` (no usa el file picker de Fyne).
- **Persistencia de config:** `voxmesh.json` junto al ejecutable (JSON plano).
- **Persistencia de historial:** JSON Lines (un mensaje por línea) en `%AppData%\VoxMesh\salas\<sala>\history`, imágenes en `.../images/`.
- **Logging de errores:** `%AppData%\VoxMesh\log-YYYY-MM-DD.log`, con `recover()` sobre panics y poda automática (>48h) al iniciar.
- **Serialización de protocolo de sala/chat:** JSON (`room.Envelope`), no binario.

## Modelo de red y salas

- **Sala (`internal/room`):** identificada por nombre, con `Epoch` (versión de host), `HostID` y una lista ordenada de `Participant{ID, Username, Address, Order, Connected, LastSeenUTC}`. `ParticipantID` es `sha256(username|address)` truncado, no la IP sola.
- **Descubrimiento:** el cliente conecta por `IP:puerto` UDP directo (`PacketRoomHello`); no hay servidor de rendezvous ni STUN todavía. Depende de que el puerto del host sea alcanzable (UPnP o port-forward manual).
- **Failover de host:** si el cliente no recibe ping del host en 6s, marca al host desconectado, calcula el siguiente `Participant` conectado por `Order` (`NextHost`/`ElectNextHost`), espera 6s a que ese candidato asuma, y si no lo hace pasa al siguiente. Solo el candidato elegido crea la sala de nuevo con el mismo nombre e incrementa `Epoch`. Todo el proceso se loguea como mensajes `[SISTEMA]` en el chat.
- **Protocolo (`internal/room/protocol.go`):** `Envelope` JSON con `Kind` (`hello`, `state`, `chat`, `sync_request`, `sync_response`, `system`, `host_claim`, `reconnect`), llevado sobre paquetes UDP tipados (`PacketRoomHello`, `PacketRoomState`, `PacketChat`, `PacketChatSync`, `PacketAudio`, `PacketPing`/`PacketPong`).
- **Historial (`internal/history`):** por sala, deduplicado por ID de mensaje. Al reconectar, cada peer intercambia `KnownIDs` y solo recibe los mensajes/imágenes que le faltan (`MessagesMissingFrom`); nunca se reenvía el historial completo. Las imágenes viajan embebidas en el mismo paquete UDP (límite práctico ~48 KiB por imagen, sin fragmentación).

## Estado de esta base

- Host: abre un puerto UDP efímero, consulta la IP pública mediante `api.ipify.org` y ofrece copiar `IP:puerto`.
- Cliente: pega `IP:puerto`, entra a la sala por nombre y se sincroniza con el estado y el historial existente.
- Host: aprende los participantes por UDP, retransmite voz, chat y estado de sala a todos.
- Audio: incluye RNNoise con VAD, Noise Gate, PCM mono de 48 kHz, enumeración de dispositivos Windows y pruebas de grabación/reproducción mediante `malgo`. Un indicador circular junto al nombre propio se enciende en verde (`#80EF80`) cuando un frame de voz válido se transmite.
- Chat: texto (Enter o botón Enviar) e imágenes (botón 📎, diálogo nativo de Windows), con eventos de sistema (conexión, desconexión, migración de host) intercalados.
- Configuración: `voxmesh.json` junto al ejecutable guarda nombre, modo de micrófono, umbral, dispositivos y último peer. `phone_mic_buffer_enabled` activa el buffer del micrófono de celular y `phone_mic_buffer_ms` define su duración (100 ms por defecto; entre 40 y 1000 ms). El enlace del celular conserva token, puerto TCP `47831` y certificado local en `%AppData%\VoxMesh`, para que una pestaña ya vinculada reconecte al reiniciar la app mientras no cambie la IP local.
- UPnP: intenta crear un mapeo UDP de una hora; si falla, la sala sigue funcionando en red local y se puede hacer port-forwarding manual.

La pantalla principal se centra en la sala y los usuarios conectados. El engranaje concentra nombre, modo de micrófono, sensibilidad, dispositivos, medidor en dBFS y pruebas de grabación/reproducción. Al crear o conectar una sala se abre el micrófono y la salida: la captura pasa por RNNoise + VAD + Noise Gate, se descartan frames sin voz y los demás viajan por UDP para reproducirse en los participantes. `Aplicar filtro de voz` permite conservar PCM crudo. Para producción conviene usar Opus, autenticación y cifrado de los datagramas.

El filtro de voz usa RNNoise para supresión espectral y una probabilidad VAD. Después aplica el Noise Gate por RMS; solo los frames con voz superando el umbral VAD se consideran transmisibles. Todavía no hay cancelación de eco ni compresor/limitador.

## Estructura

```text
cmd/voxmesh/main.go       Entrada del ejecutable
internal/config           Configuración JSON persistente (voxmesh.json)
internal/netinfo          IP pública, IP local y UPnP
internal/audio            Noise gate, PCM, dispositivos y pruebas de audio
internal/transport        Paquetes binarios y socket UDP
internal/room             Estado de sala, participantes, elección de host, protocolo (Envelope)
internal/history          Historial local por sala (JSON Lines) + imágenes, deduplicación y sync incremental
internal/logging          Logger de errores/panics en %AppData%\VoxMesh con poda de logs viejos
internal/ui               Ventana Fyne host/cliente, selector nativo de Windows, chat
```

## Preparar y ejecutar

Instala Go 1.26 o superior. En Windows, la GUI de Fyne necesita también GCC/MinGW-w64.

Para instalarlo en Windows:

1. Ejecuta en **PowerShell**:

```powershell
winget install --id MSYS2.MSYS2
```

2. Abre desde el menú Inicio la aplicación **MSYS2 UCRT64** y ejecuta allí:

```bash
pacman -Syu
pacman -S --needed mingw-w64-ucrt-x86_64-gcc
```

Si `pacman -Syu` pide cerrar la terminal, ciérrala, abre otra vez **MSYS2 UCRT64** y ejecuta el segundo comando.

Después vuelve a **PowerShell**, situado en la carpeta del proyecto:

```powershell
go mod tidy
$env:CGO_ENABLED = "0"
go test ./...
```

El test headless anterior evita depender de OpenGL/GLFW y de un compilador C. Para ejecutar la GUI real, instala MinGW-w64 y usa CGO habilitado:

```powershell
$env:CGO_ENABLED = "1"
$env:CC = "C:\msys64\ucrt64\bin\gcc.exe"
$env:Path = "C:\msys64\ucrt64\bin;$env:Path"
go run ./cmd/voxmesh
```

El ejecutable producido con `CGO_ENABLED=0` solo sirve para validar los paquetes sin GUI; al abrirlo muestra un aviso explicando cómo generar la versión gráfica.

Para generar el `.exe` gráfico final, elimina el binario anterior y compila con CGO. MinGW solo es necesario en el PC que compila; tus amigos no necesitan Go, MSYS2 ni DLLs adicionales:

```powershell
$ErrorActionPreference = "Stop"
Remove-Item .\dist\voxmesh.exe -ErrorAction SilentlyContinue
$env:CGO_ENABLED = "1"
$env:CC = "C:\msys64\ucrt64\bin\gcc.exe"
$env:Path = "C:\msys64\ucrt64\bin;$env:Path"
$build = 2
go build -trimpath -ldflags "-s -w -H windowsgui -linkmode external -extldflags -static -X voxmesh/internal/version.Value=0.9.0-build_$build" -o dist/voxmesh.exe ./cmd/voxmesh
```

El archivo para compartir es `dist\voxmesh.exe`. Incrementa `$build` en cada compilación para identificarla en la esquina inferior derecha y en el log de inicio. No compartas el ejecutable generado con `CGO_ENABLED=0`, porque es la variante de tests sin interfaz.

## Red doméstica y seguridad

La IP pública no implica que el router acepte tráfico entrante. UPnP puede estar deshabilitado; en ese caso hay que reenviar el puerto UDP mostrado por la app hacia el PC host. La versión de producción debería añadir una clave de sala, autenticación de paquetes, cifrado AEAD y límites de tamaño antes de exponerla a Internet.

