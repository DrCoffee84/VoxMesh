# VoxMesh

Chat de voz y texto para grupos chicos de amigos, en **Windows**, en un único `voxmesh.exe` sin instalador. Uno abre una **sala**, pasa la **invitación** por WhatsApp, los demás la pegan y, si el host se va o se le corta internet, la sala **pasa sola a otro participante** sin que nadie tenga que reconectarse a mano.

> 🔒 **Seguridad.** Cada "Iniciar sala" genera una clave nueva (solo en memoria) que viaja dentro de la invitación. Todo lo de la sala (voz, chat, imágenes, sonidos, control) va cifrado y autenticado con esa clave: sin la invitación nadie puede escuchar ni entrar, aunque sepa la `IP:puerto`. **Quien tenga la invitación puede entrar**: pasala solo a tus amigos. Lo que no se oculta son las IPs de los participantes.

## Qué tiene

**Voz**
- Opus a 48 kHz mono (40 kbps por defecto, con FEC), un paquete cada 20 ms.
- Filtro de voz: supresión de ruido con IA (RNNoise) + detector de voz (VAD), "modo grito" (deja pasar la voz fuerte sin aplastarla), y opcionales: puerta de ruido, ecualizador, compresor/expansor y limitador.
- **Cancelación de eco para quien usa parlantes o la tele** (WebRTC AEC3): si los demás se escuchan a sí mismos cuando hablás, activá "Uso parlantes: cancelar eco" en Ajustes → Entrada → Configuración avanzada → Supresión de ruido y voz. Corre solo en tu PC (los demás no tienen que hacer nada) y también sirve con el celular como micrófono. Con auriculares dejalo apagado: no hace falta.
- Perfiles de **entrada** y **salida** guardables ("Default" se actualiza solo con cada versión).
- Buffer de recepción adaptativo por participante: se agranda solo si la red de alguien es inestable y oculta los paquetes perdidos.
- Medidor en Ajustes que muestra qué pasa con tu voz: gris = silencio, amarillo = hay sonido pero no se manda, verde = se manda, rojo = modo grito. Avisa si el micrófono satura.
- Volumen por participante (se recuerda por nombre), botón de mute e indicador de quién habla, con ping, pérdida y jitter de cada uno.
- **Celular como micrófono**: por la página web que abre el QR (misma red Wi-Fi) o con la app Android *VoxMesh Mic* (`voxmesh-mic.apk` en cada release).
- Si se desenchufa un dispositivo o la PC vuelve de suspender, el audio se reabre solo. Si el dispositivo guardado ya no existe, se usa el predeterminado de Windows.

**Texto y sonidos**
- Chat con imágenes (botón 📎 o pegar con Ctrl+V; PNG, JPG, GIF o WebP, hasta 8 MiB) e historial por sala que se sincroniza al reconectar.
- Botonera de sonidos (MP3/WAV, con recorte de hasta ~87 s) con atajos de teclado globales, que también funcionan con un juego en primer plano. Se sincroniza con la sala: los audios se bajan cuando alguien los usa.

**App**
- Se actualiza sola desde GitHub Releases (pregunta antes, verifica el SHA-256 publicado y cierra bien la sala antes de reiniciar).
- Logs diarios en `%AppData%\VoxMesh` (botón 📄), pensados para poder mandarlos cuando algo falla.

**Todavía no tiene**: push-to-talk (anotado en [AGENDA.md](AGENDA.md)).

## Cómo se usa

1. **Host**: "Iniciar sala" → elegí un nombre (o una sala anterior, con su historial) → **Invitar** (arriba, al lado del micrófono) → "Internet" → pegá la invitación (`VM1-…`) en WhatsApp.
2. **Los demás**: copian la invitación, la pegan en el cuadro de "Cliente" y aprietan "Conectar". El nombre de la sala lo manda el host. Con una invitación equivocada o vieja aparece "Código incorrecto o host inalcanzable" a los 10 s.
3. **Cualquiera de la sala puede invitar** con el mismo botón: sirve para el que llega tarde, o para el que cerró la app (incluso si era el host) y quiere volver. La invitación sigue valiendo aunque la sala haya cambiado de host; deja de valer cuando alguien vuelve a apretar "Iniciar sala".
4. **Por LAN o VPN** (Tailscale, ZeroTier, Radmin: sin abrir puertos): el host aprieta **Invitar** y elige la opción "LAN/VPN" de la interfaz que corresponda (por ejemplo `Tailscale · 100.x.y.z:47830`).
5. Ajustes (engranaje): nombre, dispositivos, filtro de voz, perfiles, volumen máximo por usuario, celular como micrófono, puertos, migración de host y nivel de log. Los cambios se escuchan en vivo, pero se guardan recién con **Guardar**.

### Red

- UDP puro, puerto **47830** por defecto (configurable). El micrófono del celular usa el **47831** (TCP + UDP, solo red local).
- Al abrir la sala, la app intenta abrir el puerto en el router por **UPnP** (8 horas, se borra al cerrar la sala o la app) y averigua la IP pública con `api.ipify.org`. Si el router no tiene UPnP, hay que reenviar a mano el puerto UDP hacia la PC del host. En red local funciona sin nada de esto.
- Todo pasa por el host: cada cliente le manda su voz y el host la reenvía a los demás.
- **Si el host se corta**: a los 6 s los clientes muestran "Reconectando…"; si vuelve antes de 20 s, no pasa nada. Si se fue (o pasan los 20 s), el siguiente participante de la lista se vuelve host y los demás se pasan solos. Quien no quiera ser host de respaldo puede destildar "Permitir ser Host de respaldo si el Host se desconecta" en Ajustes.
- Limitación: si los dos lados están detrás de NAT simétrico o CGNAT (algunos proveedores móviles o de fibra), la conexión directa no funciona y la app avisa.
- **Todos tienen que usar la misma versión**: el formato de los paquetes (cifrado, audio) no es compatible entre versiones viejas y nuevas.

### Dónde guarda cosas

| Qué | Dónde |
|-----|-------|
| Configuración | `voxmesh.json` junto al `.exe` |
| Historial, imágenes y sonidos por sala | `%AppData%\VoxMesh\salas\<sala>\` |
| Perfiles de audio | `%AppData%\VoxMesh\perfiles\entrada\` y `...\salida\` |
| Logs (se borran a las 48 h) | `%AppData%\VoxMesh\log-AAAA-MM-DD.log` |
| Certificado y token del celular | `%AppData%\VoxMesh\` |

Nivel de log (Ajustes → Interfaz): **1 Normal** (por defecto) registra eventos, errores y, del audio, solo los intervalos con problemas, más un resumen cada 5 minutos por participante. **2 Avanzado** agrega cada intervalo de 10 s, las esperas largas de locks y un CSV con la llegada de cada paquete de audio (`llegadas-*.csv`), que se puede reproducir en los tests del buffer.

## Compilar

Requisitos: **Go 1.26+** y **MSYS2 UCRT64** (gcc, libopus y pkg-config; meson, ninja y abseil para compilar la cancelación de eco). Fyne, malgo y WebRTC AEC3 necesitan CGO. El `.exe` resultante es estático: tus amigos no necesitan instalar nada.

1. Instalar MSYS2 (PowerShell): `winget install --id MSYS2.MSYS2`
2. En la terminal **MSYS2 UCRT64**:
   ```bash
   pacman -Syu
   pacman -S --needed mingw-w64-ucrt-x86_64-gcc mingw-w64-ucrt-x86_64-opus mingw-w64-ucrt-x86_64-pkgconf \
     mingw-w64-ucrt-x86_64-meson mingw-w64-ucrt-x86_64-ninja mingw-w64-ucrt-x86_64-abseil-cpp
   ```
   (Si `pacman -Syu` pide cerrar la terminal, cerrala, abrila de nuevo y corré el segundo comando.)
3. Compilar desde PowerShell, en la carpeta del proyecto:
   ```powershell
   .\build.ps1
   # → dist\voxmesh.exe, con versión "<último tag>-dev.<fecha>" (los builds dev no buscan actualizaciones al arrancar)
   ```
   La primera vez baja y compila WebRTC Audio Processing 1.3 como biblioteca estática (`third_party\webrtc-apm\build.ps1`, menos de un minuto); después, solo si cambia abseil o el script.

A mano, o para correr los tests (antes, una vez: `.\third_party\webrtc-apm\build.ps1`; sin esa biblioteca el paquete de audio no linkea):

```powershell
$env:CGO_ENABLED = "1"
$env:CC = "C:\msys64\ucrt64\bin\gcc.exe"
$env:Path = "C:\msys64\ucrt64\bin;$env:Path"
go build -tags nolibopusfile -o bin/voxmesh.exe ./cmd/voxmesh
go test -tags nolibopusfile ./...
go test -race -tags nolibopusfile ./internal/audio/
```

- **`-tags nolibopusfile` va siempre** (build, test y vet), si no la librería de Opus pide también libopusfile. En VS Code: `"go.buildTags": "nolibopusfile"`.
- **Ojo con `CGO_ENABLED=0`**: `go build` y `go test` "andan", pero sin la interfaz ni el motor de audio (los archivos con CGO se saltean en silencio). Ese `.exe` solo muestra un aviso. Verificá `go env CGO_ENABLED` = `1`.
- El primer build con CGO tarda varios minutos.

### Publicar una versión

```powershell
.\release.ps1 v0.17.1 "Notas del release"
```

Crea el tag y lo pushea. GitHub Actions ([release.yml](.github/workflows/release.yml)) compila `voxmesh.exe` (con su `voxmesh.exe.sha256`) y `voxmesh-mic.apk` y los publica en Releases, y las apps instaladas ofrecen la actualización. El CI todavía no corre tests: correlos localmente antes de publicar.

## Estructura

```text
cmd/voxmesh/        Entrada del ejecutable (limpia restos del updater y arranca la UI)
internal/audio/     Captura/reproducción (malgo), cancelación de eco (AEC3), filtros, RNNoise, Opus, buffer de recepción, mezclador
internal/ui/        Interfaz Fyne, red de la sala, chat, botonera, ajustes
internal/room/      Estado de la sala, protocolo y migración de host (máquina de estados pura)
internal/transport/ Socket UDP, paquetes cifrados con la clave de la sala (AES-256-GCM), cola con ritmo para archivos
internal/invite/    Código de invitación (dirección del host + clave)
internal/assets/    Partir y rearmar archivos en chunks UDP
internal/history/   Historial, imágenes y sonidos por sala
internal/config/    voxmesh.json, tabla de parámetros de audio, perfiles
internal/netinfo/   IP local/pública y UPnP
internal/phonemic/  Servidor del celular como micrófono
internal/updater/   Actualización desde GitHub Releases
internal/logging/   Log diario, niveles, recuperación de panics
internal/winproc/   Prioridad del proceso y evitar suspensión en sala (Windows)
apk/                App Android "VoxMesh Mic"
third_party/webrtc-apm/ Script y parches para compilar WebRTC Audio Processing (AEC3) estática
```

## Documentación

- [AGENT.md](AGENT.md): mapa técnico detallado (cadena de audio, protocolo, hilos, reglas para tocar el código). Es lo primero que hay que leer antes de cambiar algo.
- [AGENDA.md](AGENDA.md): pendientes, uno por ID, con el problema, el arreglo y los tests de cada uno.
