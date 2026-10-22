Pendientes de Desarrollo - VoxMesh

> **Estado 2026-10-22:** la agenda anterior (volumen por usuario, optimización del pipeline, Opus, jitter adaptativo) está terminada y se borró de acá; el detalle quedó en [AGENT.md](AGENT.md) y en el historial de git.

# 1. Audio configurable + perfiles ✅ (falta probar en sala)

Objetivo: que cada usuario pueda ajustar su micrófono y su salida sin recompilar, partiendo de un default razonable, y guardar/volver a configuraciones buenas con perfiles. Todo implementado y con tests (CGO, `-race` en audio/config/phonemic/ui). El detalle técnico está en [AGENT.md §3.I](AGENT.md).

**Decisiones tomadas**
- Defaults = la configuración de Coffee + los valores que había en el código para lo demás. Entrada: filtro de voz + RNNoise + VAD + modo gritos (20 % de voz original). Salida: todo apagado (se escucha bien así).
- Perfiles de entrada y de salida separados, infinitos, un archivo por perfil en `%AppData%\VoxMesh\perfiles\entrada\` y `...\salida\`. No guardan dispositivos (quedan en `voxmesh.json`), así un perfil se pasa a otra PC tal cual.
- Sin logs de diagnóstico nuevos.

**Hecho**
- [x] Fase 1 – Modo gritos ([shout.go](internal/audio/shout.go)): check, volumen de entrada (RMS, mismo número que el medidor), voz original al gritar, hold; intensidad de RNNoise. La señal original va alineada con RNNoise (retardo de 960 muestras), RNNoise nunca se saltea.
- [x] Fase 2 – Una sola tabla de parámetros ([params.go](internal/config/params.go)): default, rango, paso, unidad, texto y perfil de cada ajuste. De ahí salen `Default()`, el control de rango al cargar, los perfiles y los controles de Ajustes. Puertos/nombres por defecto en constantes de `config`; `config.AppDir()` reemplaza las 7 copias de `UserConfigDir + "VoxMesh"`; se borró la copia de defaults de `audio.DefaultFilterSettings`.
- [x] Fase 3 – Constantes → parámetros en vivo: techo y release de envío, pre-roll, fade-in, Q del notch, Opus (bitrate, complejidad, FEC, pérdida, modo música; se aplica en vivo), jitter buffer (mínimo, percentil, margen, PLC, pausa de frase, deriva), limitador de la mezcla, volumen máximo por usuario, deriva del celular. Arreglado el bug de §3.H: pasa-altos/notch guardan su propia entrada y compresor/expansor tienen envolvente con attack/release (ya no deforman la onda).
- [x] Fase 4 – Perfiles ([profiles.go](internal/config/profiles.go)): Default fijo, Guardar / Guardar como / Renombrar / Borrar, "(modificado)", cargar = borrador en vivo (se escucha y se puede descartar). Migración: la primera vez se guarda la configuración actual como "Mi configuración" (entrada y salida).
- [x] Fase 5 – UI en acordeón ([ui_audio_params.go](internal/ui/ui_audio_params.go)): controles generados desde la tabla. El buffer del celular y el buffer de recepción se movieron a Entrada / Salida → Configuración avanzada.
- [x] Fase 6 – Defaults nuevos (los `voxmesh.json` existentes conservan sus valores).
- [x] Fase 7 – Tests (tabla, perfiles, migración, modo gritos, compresor, pasa-altos, tuning del jitter, secciones de la UI) y AGENT.md.

**Verificación en sala real (no es código)**
- [ ] Abrir Ajustes: ver que se creó "Mi configuración" en entrada y salida con los valores de antes.
- [ ] Probar perfiles: Guardar como, cambiar entre perfiles escuchando el micrófono, Descartar (tiene que volver todo), Renombrar, Borrar.
- [ ] Gritar en sala con el modo gritos y ajustar volumen/mezcla.
- [ ] Opus: subir/bajar bitrate en sala y ver que el otro sigue escuchando sin cortes.
- [ ] Buffer de recepción: con el log nivel 1, ver que el objetivo respeta el mínimo/máximo elegidos.

# 2. Más adelante

- [ ] **Mover `voxmesh.json` a `%AppData%\VoxMesh\`** (junto a `salas\` y `perfiles\`), para que la app sea solo el `.exe`. Migración: si existe el json al lado del exe y no en AppData, moverlo. Revisar [config.Path](internal/config/config.go), el updater (no tiene que tocar el json) y el README. Ojo con la migración de perfiles de `ui.New`: decide "había config" mirando `cfgPath`, tiene que mirar la ruta nueva y la vieja.
