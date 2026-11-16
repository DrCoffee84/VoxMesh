package presence

import (
	"net/netip"
	"sort"
	"strings"
	"sync"
	"time"
)

// Topes de la lista: un desconocido de la red que manda avisos inventados llena a lo sumo
// MaxPeers renglones, y desde una sola IP no más de MaxPeersPerIP (dos instancias en una PC
// alcanzan de sobra).
const (
	PeerTTL       = 10 * time.Second // sin avisos por este tiempo, sale de la lista
	MaxPeers      = 20
	MaxPeersPerIP = 4
)

// Peer es alguien de la red con VoxMesh abierto.
type Peer struct {
	ID     ID
	Name   string
	InRoom bool
	Addr   netip.AddrPort // de dónde llegó su aviso: su socket directo, adonde van las llamadas
}

// Roster es la lista de quién está en la red (en memoria, nunca a disco). Segura para varias
// goroutines.
type Roster struct {
	mu    sync.Mutex
	self  ID
	ttl   time.Duration
	peers map[ID]*rosterEntry
}

type rosterEntry struct {
	Peer
	seen time.Time
}

// NewRoster arma una lista vacía que ignora los avisos de self (los propios vuelven por el
// broadcast) y saca a quien no avisa en ttl.
func NewRoster(self ID, ttl time.Duration) *Roster {
	return &Roster{self: self, ttl: ttl, peers: map[ID]*rosterEntry{}}
}

// Seen anota un aviso de id que llegó desde from. Devuelve si cambió lo que se muestra (alguien
// nuevo, otro nombre u otro estado). Un ID vivo no cambia de dirección: si no, cualquiera de la red
// que copiara el ID de un aviso se quedaría con las llamadas a esa persona. Con otra dirección
// recién entra cuando el registro viejo se venció.
func (r *Roster) Seen(id ID, name string, inRoom bool, from netip.AddrPort, now time.Time) bool {
	if id == r.self || id.IsZero() {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if entry, ok := r.peers[id]; ok {
		if entry.Addr != from {
			if now.Sub(entry.seen) <= r.ttl {
				return false
			}
			delete(r.peers, id) // vencido: ya no es de nadie
		} else {
			entry.seen = now
			changed := entry.Name != name || entry.InRoom != inRoom
			entry.Name, entry.InRoom = name, inRoom
			return changed
		}
	}
	if len(r.peers) >= MaxPeers || r.countIP(from.Addr()) >= MaxPeersPerIP {
		return false
	}
	r.peers[id] = &rosterEntry{Peer: Peer{ID: id, Name: name, InRoom: inRoom, Addr: from}, seen: now}
	return true
}

// Bye saca a id si el "me voy" llegó desde la misma dirección que sus avisos.
func (r *Roster) Bye(id ID, from netip.AddrPort) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.peers[id]
	if !ok || entry.Addr != from {
		return false
	}
	delete(r.peers, id)
	return true
}

// Expire saca a los que no avisaron en ttl. Devuelve si sacó a alguien.
func (r *Roster) Expire(now time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	removed := false
	for id, entry := range r.peers {
		if now.Sub(entry.seen) > r.ttl {
			delete(r.peers, id)
			removed = true
		}
	}
	return removed
}

// List es una copia de la lista, por nombre (sin distinguir mayúsculas) y, con el mismo nombre,
// por ID: el orden no salta entre un aviso y otro.
func (r *Roster) List() []Peer {
	r.mu.Lock()
	peers := make([]Peer, 0, len(r.peers))
	for _, entry := range r.peers {
		peers = append(peers, entry.Peer)
	}
	r.mu.Unlock()
	sort.Slice(peers, func(i, j int) bool {
		a, b := strings.ToLower(peers[i].Name), strings.ToLower(peers[j].Name)
		if a != b {
			return a < b
		}
		return peers[i].ID.String() < peers[j].ID.String()
	})
	return peers
}

func (r *Roster) countIP(ip netip.Addr) int {
	count := 0
	for _, entry := range r.peers {
		if entry.Addr.Addr() == ip {
			count++
		}
	}
	return count
}
