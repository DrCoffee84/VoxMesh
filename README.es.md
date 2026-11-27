[English](README.md)

# VoxMesh

En la app se llama **La app de Coffee**. Chat de voz y texto para jugar o charlar con amigos, en **Windows**. Es un solo programa: sin instalador, sin cuentas y sin servidores de por medio. Uno abre una sala, pasa la invitación por WhatsApp y listo. Si el que abrió la sala se va o se le corta internet, la sala sigue sola con los demás.

## Descargar

- 💻 **PC (Windows 10 u 11):** [**Descargar VoxMesh para Windows**](https://github.com/DrCoffee84/VoxMesh/releases/latest/download/voxmesh.exe)
- 📱 **Celular como micrófono** (Android, opcional): [**Descargar VoxMesh Mic**](https://github.com/DrCoffee84/VoxMesh/releases/download/v1.0.0/voxmesh-mic.apk)

La app de la PC se actualiza sola: cuando sale una versión nueva, te pregunta si querés instalarla. **Todos los de la sala tienen que tener la misma versión.** Para ver cuál tenés, tocá la versión abajo a la derecha (o Ajustes → Sistema → **Acerca de la app de Coffee**). Ahí también está **tu ID** (algo como `K7QF-2M9X`): es de esta instalación, no de tu nombre, y es como VoxMesh te reconoce.

## Instalar en la PC

1. Creá una carpeta (por ejemplo `Documentos\VoxMesh`) y mové ahí `voxmesh.exe`. La app guarda su configuración al lado del `.exe`.
2. Abrilo con doble clic. La primera vez pueden aparecer estos avisos, que son normales (más abajo se explica [por qué](#es-seguro)):
   - **El navegador** dice que el archivo "no se descarga con frecuencia" → **Conservar**.
   - **"Windows protegió su PC"** (cartel azul) → **Más información** → **Ejecutar de todas formas**.
   - **Firewall de Windows** → **Permitir acceso**. Si no lo permitís, tus amigos no van a poder entrar a tu sala.

## Instalar la app del celular (opcional)

Sirve para usar el celular como micrófono de la PC. También se puede sin instalar nada: en la PC, Ajustes (⚙️) → **Celular** → **Mostrar QR**, y escanealo con la cámara del celular (los dos tienen que estar en el mismo Wi-Fi).

1. Abrí el link de descarga **desde el celular**.
2. Android te va a pedir permiso para instalar apps desde el navegador → **Configuración** → activá **Permitir de esta fuente** → volvé atrás.
3. Si aparece **Play Protect** avisando que la app es desconocida → **Más detalles** → **Instalar de todas formas**.
4. Abrí *VoxMesh Mic*, tocá **Escanear Código QR** y apuntá al QR que muestra la PC.

Si una versión nueva no se instala encima de la vieja, desinstalá la anterior y probá de nuevo.

## Cómo se usa

La primera vez que abrís la app te pide tu nombre (así te ven los demás); lo podés cambiar cuando quieras en Ajustes → Interfaz.

**Para abrir una sala:**
1. Tocá **Iniciar sala**. Ahí están tus salas guardadas: **Abrir** entra a una (cada sala tiene su propio chat y su botonera) y el tacho la borra de esta PC, después de preguntar. Para una nueva, escribí el nombre en **Nueva sala** y tocá **Crear y abrir**.
2. Arriba, al lado del micrófono, tocá **Invitar** y elegí el primer renglón, **Internet**. La invitación (`VM1-…`) queda copiada. Si el botón tiene un reloj naranja, esperá unos segundos: la app está abriendo el puerto del router y obteniendo tu IP pública (el registro de actividad dice cuándo está lista).
3. Pegala en WhatsApp para tus amigos.

**Para entrar a una sala:**
1. Copiá la invitación que te mandaron.
2. Pegala en el cuadro de **Cliente** y tocá **Conectar**.

**Bueno saber:**
- **Pasá la invitación solo a gente de confianza**: cualquiera que la tenga puede entrar.
- **El registro de actividad** (columna izquierda; dentro de la sala, tocá ☰ arriba a la izquierda) es donde la app avisa los problemas y qué hacer: el micrófono o los auriculares que no encontró, el audio que se cayó, el celular que se desconectó, un micrófono que pierde audio (típico del Bluetooth), el puerto del router o alguien que intenta entrar con una invitación vieja. Cada aviso sale una vez (como mucho uno cada 5 minutos del mismo tipo). Cuando algo dice "mirá el registro de actividad (☰)", es acá.
- **Si Invitar muestra un renglón naranja o rojo**, por internet todavía no pueden entrar, y el registro de actividad dice qué hacer. *Naranja*: está abriendo el puerto (esperá), o el router está detrás de otro. *Rojo*: no se pudo abrir el puerto ("Error al abrir el puerto"), no se pudo obtener tu IP pública o tu proveedor no deja entrar. Tocá el renglón rojo para ver el registro de actividad. Si abriste el puerto a mano en el router, usá el renglón naranja de **Internet** que queda abajo.
- **Invitar por LAN, Radmin, Tailscale o ZeroTier**: en **Invitar**, debajo de Internet, están las direcciones de tus redes, marcadas "(misma red o VPN)". Solo sirven si están en la misma red o VPN; por internet usá la de **Internet**. Si no las usás, podés ocultarlas apagando **Mostrar direcciones LAN/VPN al invitar** en Ajustes → Red.
- Cualquiera de la sala puede invitar con el mismo botón: sirve para el que llega tarde o para el que se cayó y quiere volver. La invitación deja de valer cuando alguien vuelve a tocar "Iniciar sala".
- **Llamar a los de tu red (LAN o Radmin)**: la tarjeta **En tu red** (columna izquierda) muestra quién tiene VoxMesh abierto en tu misma red o en Radmin, también con la app en la bandeja: punto verde con **Llamar**, o naranja "En una sala". **Llamar** abre tu sala (la elegida en "Ver otra sala…" o la última) y al otro le salta "Te está llamando" con un timbre, aunque tenga la app junto al reloj: **Atender** y entra, sin pasar invitaciones. Dentro de la sala, la misma lista está en **Invitar**. Por ahora no se puede llamar a alguien que ya está en una sala.
- **Entrar a la sala de alguien de tu red**: si alguien de **En tu red** está en una sala, su renglón dice cuál (en naranja, "en «Mi sala»") y tiene **Entrar**. Al otro le suena un toc toc corto y le aparece "Juan quiere entrar" arriba del chat y en el menú del ícono de la bandeja, sin que se le abra la ventana ni lo saque del juego: **Aceptar** o **Rechazar** (vence a los 2 minutos). Con **Dejar entrar siempre** marcado, la próxima vez entrás solo, sin que nadie toque nada, aunque el que lo marcó no esté: la lista es de la sala y la guardan todos los de adentro. Cualquiera de la sala te puede dejar entrar, no solo el host. El que crea la sala ya está en la lista, y con **Los invitados quedan de confianza** (en "Iniciar sala", prendido de fábrica) también los que entraron con la invitación o atendiendo una llamada: después entran solos desde **En tu red**. Si compartiste la invitación en un grupo grande, apagalo. **Con Tailscale no funciona** (no deja pasar los avisos a toda la red): ahí seguí usando la invitación. Se apaga con **Aparecer en mi red (LAN/VPN)** en Ajustes → Red. La primera vez que abras esta versión, Windows puede preguntar si VoxMesh puede usar la red: aceptá; si usás Radmin, marcá también **Públicas**.
- Ajustes (⚙️) tiene cinco pestañas: **Entrada y salida** (micrófono, auriculares y el filtro de ruido), **Celular** (el celular como micrófono), **Red** (puertos, host de respaldo, cuánto queda abierto el puerto, direcciones LAN/VPN, aparecer en tu red), **Interfaz** (tu nombre, idioma, colores, la X, barra de estado, sonidos de eventos) y **Sistema** (iniciar con Windows, logs, carpeta de datos, actualizaciones, Acerca de). Abre en la última pestaña que miraste. Los cambios se escuchan al instante, pero se guardan recién con **Guardar**.
- **El slider debajo de tu nombre** (dentro de la sala) es desde qué volumen te toma la voz: más a la derecha, tenés que hablar más fuerte para que se mande. La barrita de atrás muestra tu voz en vivo: verde cuando se manda, gris cuando no (o si estás muteado). Si se cuela el ruido del teclado, corrélo a la derecha hasta que el ruido quede gris y tu voz verde. Se guarda solo (si usabas un perfil de fábrica, en uno nuevo, "Personalizado").
- **Los sliders de volumen** (el de cada amigo y el de la botonera) siguen al oído: la mitad del recorrido se escucha como la mitad del volumen, y el 100 % tiene un "clic" fácil de encontrar. La botonera en 0 no suena y además bloquea los sonidos de los demás; al subirla se vuelven a escuchar.
- **Colores**: en Ajustes → Interfaz elegís la paleta: **Default** (la de siempre), **Naranja** o **Clara** (fondo claro). Se aplica al instante. Con **Editar colores…** armás tu propia paleta: primero movés **Tono** (gira todos los colores por la rueda) e **Intensidad** (más vivos o más apagados), después tocás el color que haya quedado mal para arreglarlo de a uno, lo probás con **Aplicar** y lo guardás. Los colores de estado (éxito, error, hablando, grito, medidor) quedan como están salvo que marques "Mover también los colores de estado".
- **Probar los filtros de a uno**: en Ajustes → Entrada y salida → Configuración avanzada, el perfil **Sin filtros** apaga todos; prendé uno por vez y escuchá qué hace. Ojo: sin la detección de voz ni la puerta de ruido, tu micrófono manda todo el tiempo. Ni **Default** ni **Sin filtros** se pisan: si cambiás algo y guardás, queda en un perfil nuevo ("Personalizado").
- **Ocultar los sliders**: el ícono de los sliders, a la derecha de **En sala** (al lado del gráfico), oculta los sliders de la barra, el volumen de cada uno y el tuyo, y la barra queda más chica. En color, se ven; en gris, están ocultos. Queda como lo dejaste al volver a abrir la app.
- **Quién es el host**: en **En sala**, debajo del nombre del host dice **Host**, delante de su lag; si el host sos vos, debajo del tuyo dice "Local · Host". Es la PC por la que pasa la sala: si se va, otro queda de host y la etiqueta pasa a él.
- **Detalle de lag**: el ícono del gráfico, a la derecha de **En sala**, muestra debajo de cada uno una tira con su lag de los últimos 5 minutos (verde: bien; amarillo, naranja y rojo: peor; una marca roja arriba: se perdió audio). Pasá el mouse para ver cada momento y tocala para ver toda la sesión. Si entraste a la sala de otro, el número con "≈" de los demás es el que mide el host. Cuando alguien tiene lag un rato largo (más de 10 segundos), el registro de actividad avisa desde cuándo y, al volver a lo normal, cuánto duró.
- **Ensordecer**: el botón de los auriculares, al lado del micrófono, deja de hacer sonar las voces y la botonera sin salir de la llamada (por ejemplo, para escuchar un audio de WhatsApp tranquilo). También te mutea, como en Discord, y al volver quedás como estabas. Los avisos de la app (alguien entró, llegó un mensaje) siguen sonando. Tocar el micrófono estando ensordecido te devuelve el sonido y el micrófono juntos.
- **Colgar** (el botón rojo, al lado de ensordecer) te saca de la sala: la app queda como al abrirla, el chat dice que no estás en ninguna sala y la botonera queda vacía (cada sala tiene sus sonidos; vuelven cuando entrás de nuevo). Para leer el chat de una sala vieja, elegila en **Ver otra sala…**, arriba del chat.
- **Los gritos se bajan solos**: si alguien grita, lo escuchás unos 10 dB más bajo, aunque él tenga otra configuración. Una voz normal no cambia. Se ajusta (o se apaga) en Ajustes → Entrada y salida → Configuración avanzada de la salida → Gritos que llegan.
- **El chat muestra los últimos 20 mensajes**, así gasta poca memoria en las PC con pocos recursos: **Ver anteriores**, arriba de todo, suma 20 más cada vez (vuelve a los últimos 20 al cambiar de sala, al colgar y al mandar la app junto al reloj). El buscador de arriba del chat busca en todo, también en los viejos, y resalta lo que encuentra; debajo, "3/15" dice en cuál estás, con flechas para recorrerlos (⏮ el más viejo, ▲ más viejo, ▼ más nuevo, ⏭ el más nuevo; Enter es ▲). Arranca en el más nuevo, el actual tiene un borde y, en el primero o el último, destella una vez. El botón del calendario, al lado del buscador, busca por fechas (Desde / Hasta, días enteros; cualquiera puede quedar vacío), junto con el texto o solo. Dejando el mouse sobre la hora de un mensaje, aparece su fecha y hora exactas ("lunes 16/11/2026 · 22:53:07").
- También tiene chat con imágenes y una botonera de sonidos con atajos de teclado. **Detener** corta el sonido solo en tu PC; **Para todos**, justo debajo, lo corta en la de todos los de la sala (sirve si alguien mandó uno largo sin querer). Los dos tienen al lado su botón para ponerles un atajo.
- **Atajos de la botonera mientras jugás**: si jugás con teclado y los atajos te molestan, el botón **Atajos: sí / no** (debajo de Detener) los apaga y los vuelve a prender. Ponele su propio atajo al lado y lo hacés sin salir del juego: es el único que sigue andando con los demás apagados, y suena un tono que baja al apagarlos y uno que sube al prenderlos. Queda como lo dejaste al volver a abrir la app. Si elegís una tecla que ya usa otro atajo, te avisa y no la toma.
- **Compartir pantalla**, pensado para ayudar a un amigo a configurar algo: el botón **Compartir** de arriba comparte tu pantalla (si tenés varias, elegís cuál, y la resolución) y los demás tocan **Ver pantalla** en tu avatar para verla en una ventana aparte, ajustada o en tamaño real para leer letra chica. Solo la ve quien la abre, y mientras compartís una barra te lo recuerda (ahí podés cambiar la resolución) y un punto rojo aparece sobre el botón de VoxMesh en la barra de tareas, aunque esté minimizado.
- **Al cerrar, queda junto al reloj**: la X no cierra VoxMesh, lo manda al área de notificaciones (al lado del reloj) y, si estás en una sala, seguís en ella. Hacé clic en su ícono para abrirlo; con el botón derecho, el menú para colgar, silenciar el micrófono, ensordecer o prender y apagar los atajos de la botonera, y **Salir** para salir del todo. La primera vez te lo avisa. Si preferís que la X cierre, apagá **Al cerrar la ventana, seguir abierto junto al reloj** en Ajustes → Interfaz. Si abrís VoxMesh de nuevo estando abierto, aparece el que ya estaba.
- **Iniciar con Windows**: en Ajustes → Sistema. Al prender la PC la app se abre sola y queda junto al reloj, sin abrir la ventana (apagada de fábrica). Si movés el exe a otra carpeta, abrilo una vez desde ahí y se acomoda solo. También se apaga desde el Administrador de tareas → Inicio.

## ¿Es seguro?

Sí, y no hace falta creerlo: se puede comprobar.

- **Es código abierto** ([licencia MIT](LICENSE)): todo el código está en este repositorio y cualquiera lo puede leer.
- **Lo compila GitHub, no una persona**: tanto el `.exe` como la app del celular los arma GitHub Actions a partir de este mismo código público. Cada compilación queda registrada en la pestaña [Actions](https://github.com/DrCoffee84/VoxMesh/actions).
- **¿Por qué avisan Windows y Android?** Porque la app no está firmada con un certificado pago (cuestan cientos de dólares por año) y no está en Play Store. Esos avisos aparecen con cualquier programa nuevo sin firma, no porque se haya detectado algo malo.
- **Qué hace en tu PC**: no se instala, no pide permisos de administrador y no tiene cuentas ni publicidad. Solo se conecta a los demás de tu sala, a GitHub (para buscar actualizaciones), a `api.ipify.org` (para saber tu IP pública cuando abrís una sala) y a tu router (para abrir el puerto solo).
- **"En tu red"**: los de tu misma red o Radmin ven tu nombre, si estás en una sala y cuál (el nombre de la sala, nunca la clave). La invitación le llega solo a quien atiende tu llamada o a quien dejás entrar, y nunca sale por internet. Cualquiera de la red puede pedir entrar a tu sala, pero solo suena y espera que contestes; entran solos únicamente los que alguien marcó con **Dejar entrar siempre**, y VoxMesh comprueba que sean ellos (cada instalación tiene su propia llave). Se apaga en Ajustes → Red.
- **Tu voz y tu chat viajan cifrados** con una clave que va dentro de la invitación: sin ella nadie puede escuchar ni entrar. Lo que sí ven los de la sala es tu dirección IP, como en cualquier llamada directa.
- **Si querés verificarlo vos**: en PowerShell, `Get-FileHash .\voxmesh.exe` tiene que dar el mismo número que [voxmesh.exe.sha256](https://github.com/DrCoffee84/VoxMesh/releases/latest/download/voxmesh.exe.sha256) (no importan las mayúsculas). También podés subirlo a [VirusTotal](https://www.virustotal.com); que uno o dos antivirus poco conocidos marquen un programa nuevo sin firma es un falso positivo común.

## Problemas comunes

- **"El host no responde"** (al entrar): pedile al host que mire su registro de actividad (☰), que dice si pudo abrir el puerto y qué hacer. Si la sala sigue abierta y el puerto está abierto, la invitación es vieja (alguien volvió a tocar "Iniciar sala"): pedí una nueva.
- **No aparece nadie en "En tu red"**: tienen que tener VoxMesh abierto (aunque sea en la bandeja) y estar en la misma red o en Radmin; con Tailscale no anda. Si es por Radmin, casi siempre es el Firewall de Windows: Radmin queda como red "Pública" y VoxMesh se permitió solo en "Privada". En Firewall de Windows → Permitir una aplicación, marcá voxmesh también en "Pública" (el registro de actividad lo avisa).
- **"Esta invitación es de LAN/VPN"**: el host copió una dirección de su red o de una VPN (Radmin, Tailscale, ZeroTier), que solo sirve si están en la misma. Si no, pedile la de **Internet**.
- **Tus amigos se escuchan a sí mismos (eco)**: pasa si usás parlantes o la tele. Activá **"Uso parlantes: cancelar eco"** en Ajustes → Entrada y salida → Configuración avanzada de la entrada → Supresión de ruido y voz. Con auriculares no hace falta.
- **Nadie puede entrar a tu sala**: mirá tu registro de actividad (☰), que dice si se pudo abrir el puerto. Si dice que tu router está **detrás de otro** (el módem del proveedor), abrí el mismo puerto también en el módem hacia tu router, o poné el módem en modo bridge o DMZ. Si el router no lo abrió solo (UPnP), activá UPnP en el router o abrí a mano el puerto UDP que dice el aviso (47830 si no lo cambiaste) hacia la IP local de tu PC, que también está en el aviso. Algunos proveedores de internet (sobre todo de datos móviles y algunas fibras) no permiten conexiones directas: que cree la sala otro, o que todos instalen [Tailscale](https://tailscale.com) (gratis) y el host invite con **Invitar** → la opción que dice **Tailscale**. Si dice que la sala está lista y aun así no entra nadie, revisá el firewall de Windows: la primera vez que abrís una sala Windows pregunta si deja pasar a VoxMesh; marcá redes privadas **y** públicas (o permitila en Seguridad de Windows → Firewall → "Permitir una aplicación").
- **Después de varias horas se cortó la llamada o no entra nadie**: el puerto que la app abre en el router dura 8 horas (el registro de actividad dice hasta qué hora y avisa cuando vence). Volvé a iniciar la sala y pasá la invitación nueva. Si juegan más tiempo, subí la duración (hasta 24 h) en Ajustes → Red.
- **"No se pudo abrir el micrófono"** (registro de actividad): Windows no tiene un micrófono que se pueda abrir (pasa cuando el predeterminado era un Bluetooth que se cortó). Igual escuchás a los demás, pero no te escuchan. Conectá un micrófono o elegí otro en Ajustes → Entrada y salida, o usá el celular como micrófono, y tocá **Reiniciar audio**. Con el celular elegido, VoxMesh no abre el micrófono de la PC.
- **Alguien no se puede conectar y los demás sí**: fijate que todos tengan la misma versión.
- **Otra cosa anda mal**: el botón del documento (abajo a la derecha) o Ajustes → Sistema → **Abrir carpeta de registros (logs)** abre la carpeta de logs. Mandale el del día a quien te pasó la app (el último de los días anteriores queda siempre, aunque pasen días sin abrirla).

## Más info

- [DESARROLLO.md](DESARROLLO.md): cómo funciona por dentro, cómo compilarlo y cómo publicar una versión.
- [AGENT.md](AGENT.md): mapa técnico detallado del código.
- [LICENSE](LICENSE): licencia MIT.
