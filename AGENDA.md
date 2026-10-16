Pendientes de Desarrollo - VoxMesh

1. Control de Audio Individual (Frontend y Backend)

[ ] Implementar la función matemática applyPerUserGain para modificar el PCM antes de reproducirlo.

[ ] Agregar un mapa seguro (sync.RWMutex) en la estructura App para guardar los niveles de volumen por usuario.

[ ] Modificar receiveLoop para interceptar el audio y aplicar el volumen específico de senderID antes de mandarlo al motor de audio.

[ ] Crear el componente visual en Fyne (Ej: un Slider que vaya de 0.0 a 3.0) debajo de cada avatar de la sala.

2. Descubrimiento de Salas (Cloudflare Workers + KV)

[ ] Crear un Cloudflare Worker que maneje peticiones GET y POST y se conecte al KV internamente.

[ ] Modificar la función New() en Go para que genere un ID único hexadecimal de la sala (room_id.txt) en lugar de depender del historial.

[ ] Al iniciar una sala (Host), enviar un POST http transparente con el roomID y el endpoint al Worker.

[ ] Al unirse a una sala (Cliente), enviar un GET al Worker usando el roomID ingresado por el usuario para obtener la IP pública y conectarse directamente.