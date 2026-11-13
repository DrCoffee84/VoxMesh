# VoxMesh: detalles técnicos

Para descargar e instalar, ver el [README](README.es.md). Acá está el detalle de qué hace la app, cómo usa la red, dónde guarda las cosas y cómo compilarla.

## Qué tiene

**Voz**
- Opus a 48 kHz mono (40 kbps por defecto, con FEC), un paquete cada 20 ms.
- Filtro de voz: supresión de ruido con IA (RNNoise) + detector de voz (VAD), "modo grito" (deja pasar la voz fuerte sin aplastarla), y opcionales: puerta de ruido, ecualizador, compresor/expansor y limitador.
- **Cancelación de eco para quien usa parlantes o la tele** (WebRTC AEC3): si los demás se escuchan a sí mismos cuando hablás, activá "Uso parlantes: cancelar eco" en Ajustes → Entrada → Configuración avanzada → Supresión de ruido y voz. Corre solo en tu PC (los demás no tienen que hacer nada) y también sirve con el celular como micrófono. Con auriculares dejalo apagado: no hace falta.
- Perfiles de **entrada** y **salida** guardables ("Default" se actualiza solo con cada versión).
- Buffer de recepción adaptativo por participante: se agranda solo si la red de alguien es inestable y oculta los paquetes perdidos.
- Medidor en Ajustes que muestra qué pasa con tu voz: gris = silencio, amarillo = hay sonido pero no se manda, verde = se manda, rojo = modo grito. Avisa si el micrófono satura.
- Volumen por participante (se recuerda por nombre), botón de mute e indicador de quién habla, con ping, pérdida y jitter de cada uno.
- **Celular como micrófono**: por la página web que abre el QR (misma red Wi-Fi) o con la app Android *VoxMesh Mic* (link en el [README](README.es.md#descargar); el código está en [apk/](apk/)).
- Si se desenchufa un dispositivo o la PC vuelve de suspender, el audio se reabre solo. Si el dispositivo guardado ya no existe, se usa el predeterminado de Windows.

**Texto y sonidos**
- Chat con imágenes (botón 📎 o pegar con Ctrl+V; PNG, JPG, GIF o WebP, hasta 8 MiB) e historial por sala que se sincroniza al reconectar.
- Botonera de sonidos (MP3/WAV, con recorte de hasta ~87 s) con atajos de teclado globales, que también funcionan con un juego en primer plano. Se sincroniza con la sala: los audios se bajan cuando alguien los usa.

**Pantalla compartida** (para dar soporte: ayudar a alguien a configurar algo viendo su escritorio)
- Botón **Compartir** arriba: elegís el monitor (si hay más de uno) y confirmás. Nunca empieza sola, y mientras compartís una barra naranja dice quién la está mirando, con un botón **Detener**.
- Los demás ven un ícono en tu avatar y el botón **Ver pantalla**, que abre una ventana aparte: por defecto ajustada a la ventana; **Tamaño real (1:1)** muestra los píxeles tal cual, con scroll, para leer letra chica. Se ve el cursor de quien comparte. Cerrar la ventana deja de mirar.
- Texto nítido primero: sin códec de video. Se mandan solo los cuadrados de 64×64 que cambiaron, sin pérdida (un cuadrado liso son 4 bytes; con pocos colores, PNG con paleta; si no, PNG común), hasta ~7 veces por segundo mientras la pantalla cambia y 2 quieta. La imagen completa va cuando alguien empieza a mirar; después, cada 2 s sale un resumen chico y quien mira pide solo lo que se le perdió. Hasta 1920 de ancho; una pantalla más grande se achica con un filtro de área, salvo que marques "Resolución completa".
- El cursor va aparte (su posición hasta 20 veces por segundo y su forma una sola vez): se mueve fluido aunque la imagen esté llegando.
- Si nadie mira no se manda nada. Si quien comparte se va o se cae, a los que miran se les cierra la ventana con un aviso. Si cambia el host, sigue sin cortarse.
- No se ve: video con DRM y algunos juegos en pantalla completa (salen negros), ni el cartel de UAC o la pantalla de bloqueo (queda la última imagen). No hay control remoto, audio de la PC ni compartir una sola ventana.
- Las capturas no se guardan en ningún lado: en el log solo quedan eventos (empezó, terminó, cuántos miran, kbps).

**App**
- En inglés (por defecto) o español: Ajustes → Interfaz → "Language / Idioma", se aplica al reiniciar. La página del celular sale en el mismo idioma.
- Se actualiza sola desde GitHub Releases (pregunta antes, verifica el SHA-256 publicado y cierra bien la sala antes de reiniciar).
- Logs diarios en `%AppData%\VoxMesh` (botón del documento, abajo a la derecha), pensados para poder mandarlos cuando algo falla.

**Todavía no tiene**: push-to-talk (anotado en [AGENDA.md](AGENDA.md)).

## Seguridad

Cada "Iniciar sala" genera una clave nueva (solo en memoria) que viaja dentro de la invitación. Todo lo de la sala (voz, chat, imágenes, sonidos, control) va cifrado y autenticado con esa clave (AES-256-GCM): sin la invitación nadie puede escuchar ni entrar, aunque sepa la `IP:puerto`. **Quien tenga la invitación puede entrar**. Lo que no se oculta son las IPs de los participantes.

## Red

- UDP puro, puerto **47830** por defecto (configurable). El micrófono del celular usa el **47831** (TCP + UDP, solo red local).
- Al abrir la sala, la app intenta abrir el puerto en el router por **UPnP** (8 horas por defecto, de 1 a 24 en Ajustes → Red y Conectividad; no se renueva y se borra al cerrar la sala o la app; el registro de actividad dice hasta qué hora queda abierto y avisa cuando vence) y averigua la IP pública con `api.ipify.org`. Si el router no tiene UPnP, hay que reenviar a mano el puerto UDP hacia la PC del host. Mientras tanto, "Invitar" muestra en naranja "Abriendo el puerto y obteniendo la IP pública…" y el registro de actividad dice cómo quedó (y qué hacer si falló). Si el puerto no se abrió, el menú muestra arriba "Error al abrir el puerto: mirá el registro de actividad" en rojo (tocarlo muestra la columna del registro) y abajo la dirección de internet en naranja, para quien abrió el puerto a mano. Si UPnP anduvo, también se le pide al router su IP externa: en `100.64.0.0/10` es CGNAT del proveedor (nadie de afuera llega) y si es privada el router está detrás de otro (falta abrir el puerto en el módem). En red local o por VPN (Radmin, Tailscale, ZeroTier) funciona sin nada de esto: "Invitar" ofrece debajo de Internet una dirección por interfaz, marcada "(misma red o VPN)". "Mostrar direcciones LAN/VPN al invitar" (Ajustes, prendido por defecto) sirve para ocultarlas.
- Todo pasa por el host: cada cliente le manda su voz y el host la reenvía a los demás.
- **Ritmo**: los archivos (imágenes y sonidos) salen por una cola con ritmo de ~2 Mbps y la pantalla compartida por otra, sumando todos los destinos (el host no pasa de eso aunque la miren varios). La pantalla va a **~20 Mbps si todos los que participan están en la misma red** (IP privada y ping de LAN, lo decide el host: la imagen completa llega en ~0,3 s) y a **~1,5 Mbps si alguno está por internet** (Tailscale y Radmin cuentan como internet). La voz no pasa por ninguna cola. Con la pantalla quieta casi no se usa nada (~4 kbps por cada uno que mira, más el cursor). Con una subida de menos de ~3 Mbps, compartir pantalla y sincronizar sonidos a la vez puede afectar la voz.
- Para probar en una sola PC: la ventana de "Ver pantalla" sale en la captura (espejo infinito) y la pantalla cambia todo el tiempo. Para medir de verdad, minimizala o ponela fuera del monitor que se comparte.
- **Si el host se corta**: a los 6 s los clientes muestran "Reconectando…"; si vuelve antes de 20 s, no pasa nada. Si se fue (o pasan los 20 s), el siguiente participante de la lista se vuelve host y los demás se pasan solos. Quien no quiera ser host de respaldo puede destildar "Permitir ser Host de respaldo si el Host se desconecta" en Ajustes.
- Limitación: si los dos lados están detrás de NAT simétrico o CGNAT (algunos proveedores móviles o de fibra), la conexión directa no funciona y la app avisa.
- **Todos tienen que usar la misma versión**: el formato de los paquetes (cifrado, audio) no es compatible entre versiones viejas y nuevas.

## Dónde guarda cosas

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
- Para medir cuánto ocupa compartir pantalla con lo que tenés en el monitor: `$env:VOXMESH_SCREEN_BENCH = "1"; go test -run TestScreenBandwidth -v ./internal/screen/` (no necesita CGO).
- **Ojo con `CGO_ENABLED=0`**: `go build` y `go test` "andan", pero sin la interfaz ni el motor de audio (los archivos con CGO se saltean en silencio). Ese `.exe` solo muestra un aviso. Verificá `go env CGO_ENABLED` = `1`.
- El primer build con CGO tarda varios minutos.

La app del celular se compila aparte: ver [apk/README.md](apk/README.md).

### Publicar una versión

```powershell
.\release.ps1 v1.0.1 "Notas del release"
```

Crea el tag y lo pushea. GitHub Actions ([release.yml](.github/workflows/release.yml)) compila `voxmesh.exe` (con su `voxmesh.exe.sha256`) y lo publica en Releases, y las apps instaladas ofrecen la actualización. El CI todavía no corre tests: correlos localmente antes de publicar.

El APK (`voxmesh-mic.apk`) **no** sale en ese release: lo compila [build-apk.yml](.github/workflows/build-apk.yml) como artifact de Actions y se adjunta a mano al release. El README apunta al de la v1.0.0; si se publica uno nuevo, actualizar ese link.

## Estructura

```text
cmd/voxmesh/        Entrada del ejecutable (limpia restos del updater y arranca la UI)
internal/audio/     Captura/reproducción (malgo), cancelación de eco (AEC3), filtros, RNNoise, Opus, buffer de recepción, mezclador
internal/ui/        Interfaz Fyne, red de la sala, chat, botonera, ajustes
internal/room/      Estado de la sala, protocolo y migración de host (máquina de estados pura)
internal/transport/ Socket UDP, paquetes cifrados con la clave de la sala (AES-256-GCM), colas con ritmo para archivos y pantalla
internal/screen/    Pantalla compartida sin interfaz: captura GDI, tiles que cambiaron, PNG, chunks, rearmado, filtro de área
internal/invite/    Código de invitación (dirección del host + clave)
internal/assets/    Partir y rearmar archivos en chunks UDP
internal/history/   Historial, imágenes y sonidos por sala
internal/config/    voxmesh.json, tabla de parámetros de audio, perfiles
internal/i18n/      Textos de la interfaz en inglés y español (en.go, es.go)
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
