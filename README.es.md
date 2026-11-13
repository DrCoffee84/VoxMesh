[English](README.md)

# VoxMesh

Chat de voz y texto para jugar o charlar con amigos, en **Windows**. Es un solo programa: sin instalador, sin cuentas y sin servidores de por medio. Uno abre una sala, pasa la invitación por WhatsApp y listo. Si el que abrió la sala se va o se le corta internet, la sala sigue sola con los demás.

## Descargar

- 💻 **PC (Windows 10 u 11):** [**Descargar VoxMesh para Windows**](https://github.com/DrCoffee84/VoxMesh/releases/latest/download/voxmesh.exe)
- 📱 **Celular como micrófono** (Android, opcional): [**Descargar VoxMesh Mic**](https://github.com/DrCoffee84/VoxMesh/releases/download/v1.0.0/voxmesh-mic.apk)

La app de la PC se actualiza sola: cuando sale una versión nueva, te pregunta si querés instalarla. **Todos los de la sala tienen que tener la misma versión.** Para ver cuál tenés, tocá la versión abajo a la derecha (o Ajustes → Interfaz y Sistema → **Acerca de VoxMesh**).

## Instalar en la PC

1. Creá una carpeta (por ejemplo `Documentos\VoxMesh`) y mové ahí `voxmesh.exe`. La app guarda su configuración al lado del `.exe`.
2. Abrilo con doble clic. La primera vez pueden aparecer estos avisos, que son normales (más abajo se explica [por qué](#es-seguro)):
   - **El navegador** dice que el archivo "no se descarga con frecuencia" → **Conservar**.
   - **"Windows protegió su PC"** (cartel azul) → **Más información** → **Ejecutar de todas formas**.
   - **Firewall de Windows** → **Permitir acceso**. Si no lo permitís, tus amigos no van a poder entrar a tu sala.

## Instalar la app del celular (opcional)

Sirve para usar el celular como micrófono de la PC. También se puede sin instalar nada: en la PC, Ajustes (⚙️) → **📱 Micrófono del Celular** → **Mostrar QR**, y escanealo con la cámara del celular (los dos tienen que estar en el mismo Wi-Fi).

1. Abrí el link de descarga **desde el celular**.
2. Android te va a pedir permiso para instalar apps desde el navegador → **Configuración** → activá **Permitir de esta fuente** → volvé atrás.
3. Si aparece **Play Protect** avisando que la app es desconocida → **Más detalles** → **Instalar de todas formas**.
4. Abrí *VoxMesh Mic*, tocá **Escanear Código QR** y apuntá al QR que muestra la PC.

Si una versión nueva no se instala encima de la vieja, desinstalá la anterior y probá de nuevo.

## Cómo se usa

La primera vez que abrís la app te pide tu nombre (así te ven los demás); lo podés cambiar cuando quieras en Ajustes.

**Para abrir una sala:**
1. Tocá **Iniciar sala** y elegí un nombre.
2. Arriba, al lado del micrófono, tocá **Invitar** y elegí el primer renglón, **Internet**. La invitación (`VM1-…`) queda copiada. Si el botón tiene un reloj naranja, esperá unos segundos: la app está abriendo el puerto del router y obteniendo tu IP pública (el registro de actividad dice cuándo está lista).
3. Pegala en WhatsApp para tus amigos.

**Para entrar a una sala:**
1. Copiá la invitación que te mandaron.
2. Pegala en el cuadro de **Cliente** y tocá **Conectar**.

**Bueno saber:**
- **Pasá la invitación solo a gente de confianza**: cualquiera que la tenga puede entrar.
- **Si Invitar muestra un renglón naranja o rojo**, por internet todavía no pueden entrar, y el registro de actividad dice qué hacer. *Naranja*: está abriendo el puerto (esperá), o el router está detrás de otro. *Rojo*: no se pudo abrir el puerto ("Error al abrir el puerto"), no se pudo obtener tu IP pública o tu proveedor no deja entrar. Tocá el renglón rojo para ver el registro de actividad. Si abriste el puerto a mano en el router, usá el renglón naranja de **Internet** que queda abajo.
- **Invitar por LAN, Radmin, Tailscale o ZeroTier**: en **Invitar**, debajo de Internet, están las direcciones de tus redes, marcadas "(misma red o VPN)". Solo sirven si están en la misma red o VPN; por internet usá la de **Internet**. Si no las usás, podés ocultarlas apagando **Mostrar direcciones LAN/VPN al invitar** en Ajustes → Interfaz y Sistema.
- Cualquiera de la sala puede invitar con el mismo botón: sirve para el que llega tarde o para el que se cayó y quiere volver. La invitación deja de valer cuando alguien vuelve a tocar "Iniciar sala".
- En Ajustes (⚙️) elegís tu nombre, micrófono y auriculares, el filtro de ruido y el volumen de cada amigo. Los cambios se escuchan al instante, pero se guardan recién con **Guardar**.
- **Ensordecer**: el botón de los auriculares, al lado del micrófono, deja de hacer sonar las voces y la botonera sin salir de la llamada (por ejemplo, para escuchar un audio de WhatsApp tranquilo). También te mutea, como en Discord, y al volver quedás como estabas. Los avisos de la app (alguien entró, llegó un mensaje) siguen sonando. Tocar el micrófono estando ensordecido te devuelve el sonido y el micrófono juntos.
- También tiene chat con imágenes y una botonera de sonidos con atajos de teclado. **Detener** corta el sonido solo en tu PC; **Detener para todos** lo corta en la de todos los de la sala (sirve si alguien mandó uno largo sin querer).
- **Compartir pantalla**, pensado para ayudar a un amigo a configurar algo: el botón **Compartir** de arriba comparte tu pantalla (si tenés varias, elegís cuál, y la resolución) y los demás tocan **Ver pantalla** en tu avatar para verla en una ventana aparte, ajustada o en tamaño real para leer letra chica. Solo la ve quien la abre, y mientras compartís una barra te lo recuerda (ahí podés cambiar la resolución) y un punto rojo aparece sobre el botón de VoxMesh en la barra de tareas, aunque esté minimizado.

## ¿Es seguro?

Sí, y no hace falta creerlo: se puede comprobar.

- **Es código abierto** ([licencia MIT](LICENSE)): todo el código está en este repositorio y cualquiera lo puede leer.
- **Lo compila GitHub, no una persona**: tanto el `.exe` como la app del celular los arma GitHub Actions a partir de este mismo código público. Cada compilación queda registrada en la pestaña [Actions](https://github.com/DrCoffee84/VoxMesh/actions).
- **¿Por qué avisan Windows y Android?** Porque la app no está firmada con un certificado pago (cuestan cientos de dólares por año) y no está en Play Store. Esos avisos aparecen con cualquier programa nuevo sin firma, no porque se haya detectado algo malo.
- **Qué hace en tu PC**: no se instala, no pide permisos de administrador y no tiene cuentas ni publicidad. Solo se conecta a los demás de tu sala, a GitHub (para buscar actualizaciones), a `api.ipify.org` (para saber tu IP pública cuando abrís una sala) y a tu router (para abrir el puerto solo).
- **Tu voz y tu chat viajan cifrados** con una clave que va dentro de la invitación: sin ella nadie puede escuchar ni entrar. Lo que sí ven los de la sala es tu dirección IP, como en cualquier llamada directa.
- **Si querés verificarlo vos**: en PowerShell, `Get-FileHash .\voxmesh.exe` tiene que dar el mismo número que [voxmesh.exe.sha256](https://github.com/DrCoffee84/VoxMesh/releases/latest/download/voxmesh.exe.sha256) (no importan las mayúsculas). También podés subirlo a [VirusTotal](https://www.virustotal.com); que uno o dos antivirus poco conocidos marquen un programa nuevo sin firma es un falso positivo común.

## Problemas comunes

- **"El host no responde"** (al entrar): pedile al host que mire su registro de actividad, que dice si pudo abrir el puerto y qué hacer. Si la sala sigue abierta y el puerto está abierto, la invitación es vieja (alguien volvió a tocar "Iniciar sala"): pedí una nueva.
- **"Esta invitación es de LAN/VPN"**: el host copió una dirección de su red o de una VPN (Radmin, Tailscale, ZeroTier), que solo sirve si están en la misma. Si no, pedile la de **Internet**.
- **Tus amigos se escuchan a sí mismos (eco)**: pasa si usás parlantes o la tele. Activá **"Uso parlantes: cancelar eco"** en Ajustes → Entrada → Configuración avanzada → Supresión de ruido y voz. Con auriculares no hace falta.
- **Nadie puede entrar a tu sala**: mirá tu registro de actividad, que dice si se pudo abrir el puerto. Si dice que tu router está **detrás de otro** (el módem del proveedor), abrí el mismo puerto también en el módem hacia tu router, o poné el módem en modo bridge o DMZ. Si el router no lo abrió solo (UPnP), activá UPnP en el router o abrí a mano el puerto UDP que dice el aviso (47830 si no lo cambiaste) hacia la IP local de tu PC, que también está en el aviso. Algunos proveedores de internet (sobre todo de datos móviles y algunas fibras) no permiten conexiones directas: que cree la sala otro, o que todos instalen [Tailscale](https://tailscale.com) (gratis) y el host invite con **Invitar** → la opción que dice **Tailscale**. Si dice que la sala está lista y aun así no entra nadie, revisá el firewall de Windows: la primera vez que abrís una sala Windows pregunta si deja pasar a VoxMesh; marcá redes privadas **y** públicas (o permitila en Seguridad de Windows → Firewall → "Permitir una aplicación").
- **Después de varias horas se cortó la llamada o no entra nadie**: el puerto que la app abre en el router dura 8 horas (el registro de actividad dice hasta qué hora y avisa cuando vence). Volvé a iniciar la sala y pasá la invitación nueva. Si juegan más tiempo, subí la duración (hasta 24 h) en Ajustes → Red y Conectividad.
- **Alguien no se puede conectar y los demás sí**: fijate que todos tengan la misma versión.
- **Otra cosa anda mal**: el botón del documento (abajo a la derecha) o Ajustes → **Abrir carpeta de registros (logs)** abre la carpeta de logs. Mandale el del día a quien te pasó la app.

## Más info

- [DESARROLLO.md](DESARROLLO.md): cómo funciona por dentro, cómo compilarlo y cómo publicar una versión.
- [AGENT.md](AGENT.md): mapa técnico detallado del código.
- [LICENSE](LICENSE): licencia MIT.
