package phonemic

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"html/template"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/skip2/go-qrcode"
	"nhooyr.io/websocket"
)

const (
	sampleRate     = 48000
	frameSamples   = 960
	frameBytes     = frameSamples * 2
	maxSampleBytes = sampleRate * 2 * 5
	frameDuration  = 20 * time.Millisecond
	minBufferMS    = 40
	maxBufferMS    = 1000
)

type Server struct {
	mu              sync.RWMutex
	listener        net.Listener
	http            *http.Server
	url             string
	token           string
	username        string
	connected       bool
	sample          []byte
	onPCM           func([]byte)
	onStatus        func(string)
	frames          chan []byte
	bufferEnabled   bool
	prebufferFrames int
	udpConn         *net.UDPConn
	done            chan struct{}
	stopOnce        sync.Once
}

func Start(token string, port int, bufferEnabled bool, bufferMS int, username string, onPCM func([]byte), onStatus func(string)) (*Server, error) {
	localIP, err := localIPv4()
	if err != nil {
		return nil, err
	}
	certificate, err := persistentCertificate(localIP)
	if err != nil {
		return nil, err
	}
	if port <= 0 {
		port = 47831
	}
	listener, err := tls.Listen("tcp4", net.JoinHostPort("", strconv.Itoa(port)), &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12})
	if err != nil {
		return nil, err
	}
	if token == "" {
		token, err = randomToken()
		if err != nil {
			_ = listener.Close()
			return nil, err
		}
	}
	if username == "" {
		username = "Usuario"
	}
	bufferMS = normalizeBufferMS(bufferMS)
	prebufferFrames := (bufferMS + int(frameDuration/time.Millisecond) - 1) / int(frameDuration/time.Millisecond)
	server := &Server{listener: listener, token: token, username: username, onPCM: onPCM, onStatus: onStatus, frames: make(chan []byte, prebufferFrames+12), bufferEnabled: bufferEnabled, prebufferFrames: prebufferFrames, done: make(chan struct{})}
	actualPort := listener.Addr().(*net.TCPAddr).Port
	server.url = fmt.Sprintf("https://%s/?token=%s", net.JoinHostPort(localIP.String(), fmt.Sprintf("%d", actualPort)), token)
	if udpAddr, err := net.ResolveUDPAddr("udp4", net.JoinHostPort("", strconv.Itoa(actualPort))); err == nil {
		if udpConn, err := net.ListenUDP("udp4", udpAddr); err == nil {
			server.udpConn = udpConn
			go server.listenUDP()
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", server.handlePage)
	mux.HandleFunc("/ws", server.handleWebSocket)
	server.http = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := server.http.Serve(listener); err != nil && err != http.ErrServerClosed {
			server.status("Servidor de micrófono detenido: " + err.Error())
		}
	}()
	if bufferEnabled {
		go server.forwardPCM()
	}
	return server, nil
}

func (s *Server) URL() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.url
}

func (s *Server) Token() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.token
}

func (s *Server) Port() int {
	return s.listener.Addr().(*net.TCPAddr).Port
}

func (s *Server) QRCode() ([]byte, error) {
	return qrcode.Encode(s.URL(), qrcode.Medium, 320)
}

func (s *Server) Connected() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.connected
}

func (s *Server) Recording() []byte {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]byte(nil), s.sample...)
}

func (s *Server) ResetRecording() {
	s.mu.Lock()
	s.sample = nil
	s.mu.Unlock()
}

func (s *Server) Stop() {
	if s == nil || s.http == nil {
		return
	}
	s.stopOnce.Do(func() {
		close(s.done)
		if s.udpConn != nil {
			_ = s.udpConn.Close()
		}
		_ = s.http.Shutdown(context.Background())
	})
}

func (s *Server) listenUDP() {
	buf := make([]byte, 2048)
	var lastSeen time.Time
	var lastSeenMu sync.Mutex

	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-s.done:
				return
			case <-ticker.C:
				lastSeenMu.Lock()
				seen := lastSeen
				lastSeenMu.Unlock()
				s.mu.RLock()
				connected := s.connected
				s.mu.RUnlock()
				if connected && !seen.IsZero() && time.Since(seen) > 4*time.Second {
					s.setConnected(false)
				}
			}
		}
	}()

	for {
		n, srcAddr, err := s.udpConn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		// Descartar de inmediato paquetes maliciosos o con prefijo incorrecto
		if n < 4 || string(buf[:4]) != "VMIC" {
			continue
		}
		// Descubrimiento LAN: "VMIC_DISCOVER" (no expone el token de seguridad)
		if n >= 13 && string(buf[:13]) == "VMIC_DISCOVER" {
			resp := fmt.Sprintf("VMIC_OFFER:%s", s.username)
			_, _ = s.udpConn.WriteToUDP([]byte(resp), srcAddr)
			continue
		}
		// Paquete de audio: "VMIC" (4 bytes) + tokenLen (1 byte) + token (tokenLen bytes) + PCM (1920 bytes)
		if n < 5 {
			continue
		}
		tokenLen := int(buf[4])
		headerSize := 5 + tokenLen
		if tokenLen <= 0 || n != headerSize+frameBytes {
			continue
		}
		packetToken := buf[5:headerSize]
		if subtle.ConstantTimeCompare(packetToken, []byte(s.token)) != 1 {
			continue
		}

		lastSeenMu.Lock()
		lastSeen = time.Now()
		lastSeenMu.Unlock()
		s.setConnected(true)
		pcm := buf[headerSize : headerSize+frameBytes]
		s.enqueueFrame(pcm)
	}
}

func (s *Server) handlePage(writer http.ResponseWriter, request *http.Request) {
	if request.URL.Query().Get("token") != s.token {
		http.NotFound(writer, request)
		return
	}
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = phoneTemplate.Execute(writer, struct{ Username string }{Username: s.username})
}

func (s *Server) handleWebSocket(writer http.ResponseWriter, request *http.Request) {
	if request.URL.Query().Get("token") != s.token {
		http.Error(writer, "invalid token", http.StatusForbidden)
		return
	}
	connection, err := websocket.Accept(writer, request, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer connection.Close(websocket.StatusNormalClosure, "closed")
	s.setConnected(true)
	defer s.setConnected(false)
	for {
		kind, frame, err := connection.Read(request.Context())
		if err != nil {
			return
		}
		if kind != websocket.MessageBinary || len(frame) != frameBytes {
			continue
		}
		s.enqueueFrame(frame)
	}
}

func (s *Server) enqueueFrame(frame []byte) {
	copyFrame := append([]byte(nil), frame...)
	if !s.bufferEnabled {
		s.deliverFrame(copyFrame)
		return
	}
	select {
	case s.frames <- copyFrame:
		return
	default:
	}
	select {
	case <-s.frames:
	default:
	}
	select {
	case s.frames <- copyFrame:
	default:
	}
}

func (s *Server) deliverFrame(frame []byte) {
	s.mu.Lock()
	s.sample = append(s.sample, frame...)
	if len(s.sample) > maxSampleBytes {
		s.sample = append([]byte(nil), s.sample[len(s.sample)-maxSampleBytes:]...)
	}
	s.mu.Unlock()
	if s.onPCM != nil {
		s.onPCM(frame)
	}
}

func (s *Server) forwardPCM() {
	ticker := time.NewTicker(frameDuration)
	defer ticker.Stop()
	playing := false
	for {
		select {
		case <-s.done:
			return
		case <-ticker.C:
			if !playing {
				if len(s.frames) < s.prebufferFrames {
					continue
				}
				playing = true
			}
			select {
			case frame := <-s.frames:
				s.deliverFrame(frame)
			default:
				playing = false
			}
		}
	}
}

func normalizeBufferMS(bufferMS int) int {
	if bufferMS < minBufferMS {
		return minBufferMS
	}
	if bufferMS > maxBufferMS {
		return maxBufferMS
	}
	return bufferMS
}

func (s *Server) setConnected(connected bool) {
	s.mu.Lock()
	changed := s.connected != connected
	s.connected = connected
	s.mu.Unlock()
	if changed {
		if connected {
			s.status("Celular conectado como micrófono.")
		} else {
			s.status("Celular desconectado del micrófono.")
		}
	}
}

func (s *Server) status(message string) {
	if s.onStatus != nil {
		s.onStatus(message)
	}
}

func localIPv4() (net.IP, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	for _, networkInterface := range interfaces {
		if networkInterface.Flags&net.FlagUp == 0 || networkInterface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addresses, err := networkInterface.Addrs()
		if err != nil {
			continue
		}
		for _, address := range addresses {
			ipNet, ok := address.(*net.IPNet)
			if !ok || ipNet.IP.To4() == nil || !ipNet.IP.IsPrivate() {
				continue
			}
			return ipNet.IP.To4(), nil
		}
	}
	return nil, fmt.Errorf("no se encontró una IP local privada")
}

func persistentCertificate(ip net.IP) (tls.Certificate, error) {
	root, err := os.UserConfigDir()
	if err != nil {
		return tls.Certificate{}, err
	}
	root = filepath.Join(root, "VoxMesh")
	if err := os.MkdirAll(root, 0700); err != nil {
		return tls.Certificate{}, err
	}
	certificatePath := filepath.Join(root, "phone-mic-cert.pem")
	keyPath := filepath.Join(root, "phone-mic-key.pem")
	if _, certErr := os.Stat(certificatePath); certErr == nil {
		return tls.LoadX509KeyPair(certificatePath, keyPath)
	} else if !os.IsNotExist(certErr) {
		return tls.Certificate{}, certErr
	}
	certificate, certificatePEM, keyPEM, err := selfSignedCertificate(ip)
	if err != nil {
		return tls.Certificate{}, err
	}
	if err := os.WriteFile(certificatePath, certificatePEM, 0600); err != nil {
		return tls.Certificate{}, err
	}
	if err := os.WriteFile(keyPath, keyPEM, 0600); err != nil {
		_ = os.Remove(certificatePath)
		return tls.Certificate{}, err
	}
	return certificate, nil
}

func selfSignedCertificate(ip net.IP) (tls.Certificate, []byte, []byte, error) {
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, nil, nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, nil, nil, err
	}
	template := x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "VoxMesh Phone Mic"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{ip},
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return tls.Certificate{}, nil, nil, err
	}
	certificate := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	key, err := x509.MarshalECPrivateKey(privateKey)
	if err != nil {
		return tls.Certificate{}, nil, nil, err
	}
	privateKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: key})
	tlsCertificate, err := tls.X509KeyPair(certificate, privateKeyPEM)
	if err != nil {
		return tls.Certificate{}, nil, nil, err
	}
	return tlsCertificate, certificate, privateKeyPEM, nil
}

func randomToken() (string, error) {
	bytes := make([]byte, 24)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

var phoneTemplate = template.Must(template.New("phone").Parse(phonePageTemplate))

const phonePageTemplate = `<!doctype html><html lang="es"><meta name="viewport" content="width=device-width,initial-scale=1"><title>VoxMesh Mic</title>
<style>
body{font:16px sans-serif;background:#142124;color:#e8f2ef;margin:0;padding:32px;text-align:center}
button{font:inherit;padding:14px 22px;background:#80ef80;border:0;border-radius:6px;color:#102014}
#state{margin-top:20px;line-height:1.5}
p.info{max-width:420px;margin:16px auto;font-size:14px;color:#b7c9c4;line-height:1.5;text-align:left}
</style>
<h1>Micrófono VoxMesh</h1>
<p>Bienvenido, {{.Username}}</p>
<p class="info">Este celular funciona como el micrófono de tu PC en la sala VoxMesh: tu voz se manda por Wi-Fi y no crea un usuario aparte. Tocá el botón, aceptá el permiso de micrófono y dejá esta pestaña abierta con la pantalla encendida mientras hablás. Si bloqueás la pantalla manualmente, la conexión se corta y se reconecta sola al desbloquear.</p>
<button id="start">Usar este celular como micrófono</button>
<div id="state">Conectá el micrófono para hablar desde la PC.</div>
<audio id="keepalive" loop playsinline style="display:none"></audio>
<script>
const state=document.querySelector('#state'),button=document.querySelector('#start'),keepAlive=document.querySelector('#keepalive'),token=new URLSearchParams(location.search).get('token');
let context,stream,processor,socket,samples=[],wakeLock,reconnecting=false;

function send(input){
	const ratio=context.sampleRate/48000;
	for(let position=0;position<input.length;position+=ratio){
		const sample=input[Math.min(input.length-1,Math.floor(position))];
		samples.push(Math.max(-1,Math.min(1,sample))*32767)
	}
	while(samples.length>=960&&socket.readyState===WebSocket.OPEN){
		const frame=samples.splice(0,960),buffer=new ArrayBuffer(1920),view=new DataView(buffer);
		frame.forEach((sample,index)=>view.setInt16(index*2,sample,true));
		socket.send(buffer)
	}
}

function connect(){
	socket=new WebSocket('wss://'+location.host+'/ws?token='+encodeURIComponent(token));
	socket.binaryType='arraybuffer';
	socket.onopen=()=>{state.textContent='Conectado. Dejá esta pantalla abierta.';reconnecting=false};
	socket.onclose=()=>{state.textContent='Reconectando...';setTimeout(resume,1000)}
}

async function requestWakeLock(){
	if(!('wakeLock' in navigator))return;
	try{
		wakeLock=await navigator.wakeLock.request('screen');
		wakeLock.addEventListener('release',()=>{wakeLock=null})
	}catch(error){}
}

function startKeepAliveAudio(){
	try{
		const silentContext=new (window.AudioContext||window.webkitAudioContext)();
		const oscillator=silentContext.createOscillator(),gain=silentContext.createGain(),destination=silentContext.createMediaStreamDestination();
		gain.gain.value=0.001;
		oscillator.connect(gain);
		gain.connect(destination);
		oscillator.start();
		keepAlive.srcObject=destination.stream;
		keepAlive.volume=0.01;
		keepAlive.play().catch(()=>{});
		if('mediaSession' in navigator){
			navigator.mediaSession.metadata=new MediaMetadata({title:'VoxMesh Micrófono',artist:'{{.Username}}'});
			navigator.mediaSession.playbackState='playing';
			navigator.mediaSession.setActionHandler('play',()=>{});
			navigator.mediaSession.setActionHandler('pause',()=>{})
		}
	}catch(error){}
}

async function reacquireMicIfNeeded(){
	if(!stream||!stream.getAudioTracks().every(track=>track.readyState==='ended'))return;
	try{
		stream=await navigator.mediaDevices.getUserMedia({audio:{channelCount:1,echoCancellation:false,noiseSuppression:false,autoGainControl:false}});
		context.createMediaStreamSource(stream).connect(processor)
	}catch(error){state.textContent='No se pudo recuperar el micrófono: '+error.message}
}

async function resume(){
	if(reconnecting)return;
	reconnecting=true;
	if(context&&context.state==='suspended')await context.resume().catch(()=>{});
	await reacquireMicIfNeeded();
	connect()
}

document.addEventListener('visibilitychange',()=>{
	if(document.visibilityState!=='visible')return;
	requestWakeLock();
	if(context&&context.state==='suspended')context.resume().catch(()=>{});
	reacquireMicIfNeeded();
	if(socket&&socket.readyState!==WebSocket.OPEN&&stream)connect()
});

button.onclick=async()=>{
	try{
		stream=await navigator.mediaDevices.getUserMedia({audio:{channelCount:1,echoCancellation:false,noiseSuppression:false,autoGainControl:false}});
		context=new AudioContext({sampleRate:48000});
		const source=context.createMediaStreamSource(stream),mute=context.createGain();
		processor=context.createScriptProcessor(2048,1,1);
		mute.gain.value=0;
		processor.onaudioprocess=event=>send(event.inputBuffer.getChannelData(0));
		source.connect(processor);
		processor.connect(mute);
		mute.connect(context.destination);
		connect();
		requestWakeLock();
		startKeepAliveAudio();
		button.disabled=true
	}catch(error){state.textContent='No se pudo usar el micrófono: '+error.message}
}
</script>`
