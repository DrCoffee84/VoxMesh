# VoxMesh Mic (Android) 📱🎙️

Mini aplicación nativa de Android para usar el celular como micrófono de alta fidelidad en **VoxMesh** sin cortes, con transmisión por UDP en tiempo real y soporte para grabar con la pantalla bloqueada o la app en segundo plano.

---

## Características

* **Cero cortes por Wi-Fi**: Transmisión de voz por **UDP directo** en bloques de 20 ms (1920 bytes PCM a 48 kHz).
* **Audio nítido de estudio**: Captura nativa a **48.000 Hz 16-bit Mono** con optimización acústica para voz.
* **Pantalla bloqueable (Bolsillo)**: Implementa un **Foreground Service** con notificación persistente (`FOREGROUND_SERVICE_MICROPHONE`) y WakeLock para que Android nunca suspenda el micrófono.
* **Escaneo de QR instantáneo**: Integrado con Google Code Scanner; lee la IP, el puerto y el token del QR de VoxMesh PC sin pedir permisos de cámara.
* **Procesamiento en la PC**: El celular no gasta batería ni CPU aplicando filtros pesados; VoxMesh en la PC se encarga de aplicar **RNNoise (IA)**, puerta de ruido, VAD y ecualización.
* **Controles**: Botón para iniciar/detener transmisión, botón de silencio (Mute) y medidor de nivel VU en vivo.

---

## Cómo compilar el APK

### Opción 1: Con Android Studio (Recomendado)
1. Abrí Android Studio.
2. Seleccioná **Open** y elegí la carpeta `VoxMesh/apk`.
3. Esperá a que Gradle sincronice las dependencias.
4. Conectá tu celular por USB (o iniciá un emulador) y presioná el botón verde **Run (▶)**, o andá a **Build > Build Bundle(s) / APK(s) > Build APK(s)**.

### Opción 2: Con línea de comandos y ADB
Si tenés Java 17 instalado en tu sistema:
```bash
cd apk
./gradlew assembleDebug
```
El archivo APK generado quedará en:
`apk/app/build/outputs/apk/debug/app-debug.apk`

Para instalarlo en tu teléfono conectado por USB con depuración habilitada:
```bash
adb install -r apk/app/build/outputs/apk/debug/app-debug.apk
```

### Opción 3: GitHub Actions (Automático)
El repositorio incluye el workflow `.github/workflows/build-apk.yml`. Al hacer push a GitHub, se compila automáticamente y podés descargar el APK listo para instalar desde la pestaña **Actions > Artifacts**.

---

## Cómo usarlo

1. Abrí **VoxMesh** en tu PC y andá a **Configuración (Ajustes)**.
2. En la tarjeta de **📱 Micrófono del Celular**, hacé clic en **Mostrar QR**.
3. Abrí la app **VoxMesh Mic** en tu celular.
4. Tocá **📷 Escanear Código QR** y apuntá a la pantalla de la PC.
5. ¡Listo! Se completarán los datos y comenzará la transmisión. Podés bloquear la pantalla de tu celular y hablar tranquilamente.
