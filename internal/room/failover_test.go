package room

import (
	"testing"
	"time"
)

var foT0 = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

const (
	hostAddr = "186.57.0.1:47825"
	anaAddr  = "190.0.0.2:47830"
	betoAddr = "181.0.0.3:47830"
	caroAddr = "200.0.0.4:47830"
)

// testRoom es una sala con host H y, en orden de llegada, Ana (A), Beto (B) y opcionalmente Caro (C).
func testRoom(withCaro bool) State {
	state := State{Name: "sala", Epoch: 1, HostID: "H", Participants: []Participant{
		{ID: "H", Username: "Host", Address: hostAddr, Order: 0, Connected: true, CanBeHost: true},
		{ID: "A", Username: "Ana", Address: anaAddr, Order: 1, Connected: true, CanBeHost: true},
		{ID: "B", Username: "Beto", Address: betoAddr, Order: 2, Connected: true, CanBeHost: true},
	}}
	if withCaro {
		state.Participants = append(state.Participants, Participant{ID: "C", Username: "Caro", Address: caroAddr, Order: 3, Connected: true, CanBeHost: true})
	}
	return state
}

// newClient es self conectado al host H, con el estado de la sala ya recibido.
func newClient(self string, state State) *Failover {
	f := NewFailover(self, DefaultFailoverTimings())
	f.JoinedAsClient(hostAddr, foT0)
	f.HostState(state, foT0)
	f.Drain()
	return f
}

// run avanza el reloj de a 250 ms en (from, to] llamando a Tick y junta las acciones. alive, si no
// es nil, dice si en ese momento llega un paquete del host.
func run(f *Failover, from, to time.Duration, alive func(time.Duration) bool) []Action {
	var actions []Action
	for at := from + 250*time.Millisecond; at <= to; at += 250 * time.Millisecond {
		if alive != nil && alive(at) {
			f.Seen(hostAddr, foT0.Add(at))
		}
		f.Tick(foT0.Add(at))
		actions = append(actions, f.Drain()...)
	}
	return actions
}

func count(actions []Action, kind ActionKind, addr string) int {
	n := 0
	for _, action := range actions {
		if action.Kind == kind && (addr == "" || action.Addr == addr) {
			n++
		}
	}
	return n
}

func find(t *testing.T, actions []Action, kind ActionKind) Action {
	t.Helper()
	for _, action := range actions {
		if action.Kind == kind {
			return action
		}
	}
	t.Fatalf("no action of kind %d in %+v", kind, actions)
	return Action{}
}

// newRoomState es el estado que manda Ana como host nuevo (epoch 2).
func newRoomState() State {
	state := testRoom(false)
	state.HostID = "A"
	state.Epoch = 2
	state.Participants[0].Connected = false
	return state
}

func TestFailover_BriefHostSilenceDoesNotMigrate(t *testing.T) {
	f := newClient("B", testRoom(false))
	// El host se calla 15 s (corte de internet) y vuelve.
	actions := run(f, 0, 15*time.Second, nil)
	if count(actions, ActStatus, "") != 1 || count(actions, ActHello, hostAddr) < 4 {
		t.Fatalf("expected one 'reconectando' notice and retries to the host: %+v", actions)
	}
	if count(actions, ActHello, anaAddr) != 0 || count(actions, ActBecomeHost, "") != 0 || count(actions, ActGiveUp, "") != 0 {
		t.Fatalf("a 15 s cut must not migrate: %+v", actions)
	}
	if f.Seen(hostAddr, foT0.Add(15*time.Second)) != FromHost {
		t.Fatal("the host's packet was not recognized")
	}
	back := f.Drain()
	if count(back, ActStatus, "") != 1 || count(back, ActHello, hostAddr) != 1 {
		t.Fatalf("expected 'volvió la conexión' and a hello to re-register: %+v", back)
	}
	// Con el host vivo no pasa nada más.
	later := run(f, 15*time.Second, 60*time.Second, func(time.Duration) bool { return true })
	if count(later, ActStatus, "") != 0 || count(later, ActHello, "") != 0 {
		t.Fatalf("nothing should happen with the host alive: %+v", later)
	}
}

func TestFailover_HostLeavesFirstCandidateTakesOver(t *testing.T) {
	f := newClient("A", testRoom(false))
	f.HostLeft(foT0)
	actions := f.Drain()
	become := find(t, actions, ActBecomeHost)
	if become.State.HostID != "A" || become.State.Epoch != 2 || become.State.Participants[0].Connected {
		t.Fatalf("new room state: %+v", become.State)
	}
	if count(actions, ActSendState, betoAddr) != 1 || count(actions, ActSendState, hostAddr) != 0 {
		t.Fatalf("expected the state sent to Beto only (the host left on purpose): %+v", actions)
	}
	// Sigue avisando hasta que Beto entra.
	if count(run(f, 0, 3*time.Second, nil), ActSendState, betoAddr) < 5 {
		t.Fatal("the new host must keep sending its state to whoever has not joined")
	}
	f.HelloFrom("B", foT0.Add(3*time.Second))
	if count(run(f, 3*time.Second, 30*time.Second, nil), ActSendState, "") != 0 {
		t.Fatal("no more announcements once everyone joined")
	}
}

func TestFailover_HostLeavesOthersJoinTheCandidate(t *testing.T) {
	f := newClient("B", testRoom(false))
	f.HostLeft(foT0)
	if count(f.Drain(), ActHello, anaAddr) != 1 {
		t.Fatal("expected an immediate hello to Ana (first candidate)")
	}
	// Los primeros estados de Ana se pierden: Beto le sigue pidiendo entrar (abre su router).
	if count(run(f, 0, 3*time.Second, nil), ActHello, anaAddr) < 5 {
		t.Fatal("expected hellos to Ana every 500 ms while waiting")
	}
	f.StateFrom(newRoomState(), anaAddr, foT0.Add(3*time.Second))
	switched := find(t, f.Drain(), ActSwitchHost)
	if switched.Addr != anaAddr || switched.State.HostID != "A" {
		t.Fatalf("expected a switch to Ana: %+v", switched)
	}
	if f.Seen(anaAddr, foT0.Add(4*time.Second)) != FromHost || f.Seen(hostAddr, foT0.Add(4*time.Second)) != FromOther {
		t.Fatal("after the switch only Ana is the host")
	}
}

func TestFailover_SilentHostWaitsForGraceBeforeMigrating(t *testing.T) {
	f := newClient("B", testRoom(false))
	if count(run(f, 0, 20*time.Second, nil), ActHello, anaAddr) != 0 {
		t.Fatal("migrated before the grace period")
	}
	if count(run(f, 20*time.Second, 21*time.Second, nil), ActHello, anaAddr) == 0 {
		t.Fatal("expected the migration to start after the grace period")
	}
}

// El amigo con Wi-Fi que entró primero pierde la conexión: no se hace host de una sala vacía.
func TestFailover_SilentCandidateNeedsSomeoneToAsk(t *testing.T) {
	f := newClient("A", testRoom(false))
	actions := run(f, 0, 60*time.Second, nil)
	if count(actions, ActBecomeHost, "") != 0 {
		t.Fatalf("a candidate nobody asked for must not become host: %+v", actions)
	}
	if count(actions, ActPing, betoAddr) == 0 {
		t.Fatal("while waiting, the candidate must ping the others to open its router")
	}
	if count(actions, ActGiveUp, "") != 1 {
		t.Fatalf("expected to give up after trying every candidate: %+v", actions)
	}
}

func TestFailover_SilentCandidateTakesOverWhenAsked(t *testing.T) {
	f := newClient("A", testRoom(false))
	run(f, 0, 21*time.Second, nil) // período de gracia vencido: turno de Ana, esperando
	f.HelloFrom("B", foT0.Add(21*time.Second))
	actions := f.Drain()
	find(t, actions, ActBecomeHost)
	// El host solo se calló: también se le avisa a él, por si vuelve.
	if count(actions, ActSendState, betoAddr) != 1 || count(actions, ActSendState, hostAddr) != 1 {
		t.Fatalf("expected the state sent to Beto and to the old host: %+v", actions)
	}
}

func TestFailover_UnreachableCandidateGoesToTheNextOne(t *testing.T) {
	// Sala de 3: Ana no le contesta a Beto. Le toca a Beto, pero nadie le pide entrar: se rinde
	// en vez de quedar como host solo.
	f := newClient("B", testRoom(false))
	f.HostLeft(foT0)
	actions := append(f.Drain(), run(f, 0, 25*time.Second, nil)...)
	if count(actions, ActBecomeHost, "") != 0 || count(actions, ActGiveUp, "") != 1 {
		t.Fatalf("expected to give up instead of hosting alone: %+v", actions)
	}

	// Sala de 4: Caro tampoco llega a Ana y le pide entrar a Beto, que es el siguiente.
	f = newClient("B", testRoom(true))
	f.HostLeft(foT0)
	f.Drain()
	run(f, 0, 10*time.Second, nil) // Ana no responde: turno de Beto
	f.HelloFrom("C", foT0.Add(11*time.Second))
	become := find(t, f.Drain(), ActBecomeHost)
	if become.State.HostID != "B" {
		t.Fatalf("expected Beto as host: %+v", become.State)
	}
}

func TestFailover_OldHostStepsDownForNewerRoom(t *testing.T) {
	f := NewFailover("H", DefaultFailoverTimings())
	f.Hosting(testRoom(false), foT0)
	// Un estado viejo o de alguien que no es de la sala no cambia nada.
	stale := newRoomState()
	stale.Epoch = 1
	stale.HostID = "Z"
	f.StateFrom(stale, anaAddr, foT0)
	if actions := f.Drain(); len(actions) != 0 {
		t.Fatalf("unexpected actions: %+v", actions)
	}
	f.StateFrom(newRoomState(), anaAddr, foT0)
	if step := find(t, f.Drain(), ActStepDown); step.Addr != anaAddr {
		t.Fatalf("expected to step down in favor of Ana: %+v", step)
	}
	if f.Seen(anaAddr, foT0) != FromHost {
		t.Fatal("after stepping down, Ana is the host")
	}
}

func TestFailover_TwoHostsWithSameEpochKeepTheLowerID(t *testing.T) {
	roomOf := func(host string) State {
		state := newRoomState()
		state.HostID = host
		return state
	}
	beto := NewFailover("B", DefaultFailoverTimings())
	beto.Hosting(roomOf("B"), foT0)
	beto.StateFrom(roomOf("A"), anaAddr, foT0)
	find(t, beto.Drain(), ActStepDown)

	ana := NewFailover("A", DefaultFailoverTimings())
	ana.Hosting(roomOf("A"), foT0)
	ana.StateFrom(roomOf("B"), betoAddr, foT0)
	if count(ana.Drain(), ActStepDown, "") != 0 {
		t.Fatal("the host with the lower ID must stay")
	}
}

func TestFailover_ClientFollowsNewerRoom(t *testing.T) {
	f := newClient("B", testRoom(false))
	same := newRoomState()
	same.Epoch = 1
	f.StateFrom(same, anaAddr, foT0)
	if actions := f.Drain(); len(actions) != 0 {
		t.Fatalf("a state with the same epoch from someone else is ignored: %+v", actions)
	}
	f.StateFrom(newRoomState(), anaAddr, foT0)
	if switched := find(t, f.Drain(), ActSwitchHost); switched.Addr != anaAddr {
		t.Fatalf("expected a switch to Ana's newer room: %+v", switched)
	}
}

func TestFailover_HostHandsTheRoomOver(t *testing.T) {
	// El host se bajó y reenvía el estado de la sala nueva: hay que ir hacia Ana.
	f := newClient("B", testRoom(false))
	f.HostState(newRoomState(), foT0)
	if count(f.Drain(), ActHello, anaAddr) != 1 {
		t.Fatal("expected a hello to the new host")
	}
	f.StateFrom(newRoomState(), anaAddr, foT0.Add(time.Second))
	find(t, f.Drain(), ActSwitchHost)
}

func TestFailover_NewHostKeepsCallingTheOldHost(t *testing.T) {
	f := newClient("A", testRoom(false))
	run(f, 0, 21*time.Second, nil)
	f.HelloFrom("B", foT0.Add(21*time.Second))
	f.Drain()
	f.HelloFrom("B", foT0.Add(22*time.Second))
	// El host viejo no volvió: se le avisa cada 500 ms los primeros 10 s, después cada 2 s hasta los
	// 2 minutos, y después no.
	if calls := count(run(f, 22*time.Second, 31*time.Second, nil), ActSendState, hostAddr); calls < 16 || calls > 19 {
		t.Fatalf("expected one call every 500 ms during the first 10 s, got %d", calls)
	}
	if calls := count(run(f, 31*time.Second, 21*time.Second+2*time.Minute, nil), ActSendState, hostAddr); calls < 53 || calls > 56 {
		t.Fatalf("expected one call every 2 s until 2 minutes, got %d", calls)
	}
	if count(run(f, 21*time.Second+2*time.Minute, 4*time.Minute, nil), ActSendState, "") != 0 {
		t.Fatal("announcements must stop after 2 minutes")
	}
}

// Me conecté a la IP pública desde la misma red que el host y la respuesta llega desde su IP
// local: esa pasa a ser la dirección del host (si no, se ignoraría todo lo que manda).
func TestFailover_FirstStateBindsTheHostAddress(t *testing.T) {
	const lanAddr = "192.168.1.39:47825"
	f := NewFailover("B", DefaultFailoverTimings())
	f.JoinedAsClient(hostAddr, foT0)
	if f.Seen(lanAddr, foT0) != FromOther {
		t.Fatal("before the first state, a different address is not the host")
	}
	f.StateFrom(testRoom(false), lanAddr, foT0)
	if bound := find(t, f.Drain(), ActSwitchHost); bound.Addr != lanAddr {
		t.Fatalf("expected the host bound to its LAN address: %+v", bound)
	}
	if f.Seen(lanAddr, foT0) != FromHost {
		t.Fatal("the LAN address is now the host")
	}
	// Ya con sala, otro estado de una dirección ajena con la misma autoridad no cambia nada.
	f.StateFrom(testRoom(false), "10.9.9.9:1", foT0)
	if actions := f.Drain(); len(actions) != 0 {
		t.Fatalf("unexpected actions: %+v", actions)
	}
}

func TestFailover_StopSilencesEverything(t *testing.T) {
	f := newClient("A", testRoom(false))
	f.Stop()
	f.HostLeft(foT0)
	f.StateFrom(newRoomState(), anaAddr, foT0)
	if actions := append(f.Drain(), run(f, 0, time.Minute, nil)...); len(actions) != 0 {
		t.Fatalf("a stopped failover must not act (the user left the room): %+v", actions)
	}
}

func TestFailover_RefusedBeforeJoiningGivesUp(t *testing.T) {
	f := NewFailover("A", DefaultFailoverTimings())
	f.JoinedAsClient(hostAddr, foT0)
	f.Refused("La sala está llena.")
	actions := f.Drain()
	if giveUp := find(t, actions, ActGiveUp); giveUp.Text != "La sala está llena." {
		t.Fatalf("unexpected give up text %q", giveUp.Text)
	}
	if actions := run(f, 0, 30*time.Second, nil); len(actions) != 0 {
		t.Fatalf("a refused client must stop trying, got %+v", actions)
	}
}

// Invitación con la clave equivocada (o host caído): el host descarta todo en silencio. El cliente
// reintenta el RoomHello, no dice "Reconectando…" ni intenta migrar, y a los Join se rinde.
func TestFailover_JoinWithoutAnswerGivesUp(t *testing.T) {
	timings := DefaultFailoverTimings()
	f := NewFailover("A", timings)
	f.JoinedAsClient(hostAddr, foT0)
	actions := run(f, 0, timings.Join, nil)
	if hellos := count(actions, ActHello, hostAddr); hellos != int(timings.Join/timings.Ping) {
		t.Fatalf("RoomHello al host = %d, quería uno cada %v", hellos, timings.Ping)
	}
	if count(actions, ActGiveUp, "") != 0 || count(actions, ActStatus, "") != 0 || count(actions, ActPing, "") != 0 {
		t.Fatalf("antes del plazo solo reintenta el RoomHello: %+v", actions)
	}
	actions = run(f, timings.Join, timings.Join+time.Second, nil)
	if giveUp := find(t, actions, ActGiveUp); giveUp.Text != JoinFailedMessage {
		t.Fatalf("aviso %q, quería %q", giveUp.Text, JoinFailedMessage)
	}
	if actions := run(f, timings.Join+time.Second, time.Minute, nil); len(actions) != 0 {
		t.Fatalf("después de rendirse no hace nada más: %+v", actions)
	}
}

// Si el primer RoomHello se perdió, el reintento entra antes del plazo y la sala sigue normal.
func TestFailover_JoinAnsweredLateStays(t *testing.T) {
	f := NewFailover("A", DefaultFailoverTimings())
	f.JoinedAsClient(hostAddr, foT0)
	run(f, 0, 3*time.Second, nil)
	at := foT0.Add(3 * time.Second)
	if f.Seen(hostAddr, at) != FromHost {
		t.Fatal("el primer paquete del host tiene que contar como del host")
	}
	f.HostState(testRoom(false), at)
	actions := run(f, 3*time.Second, 30*time.Second, func(time.Duration) bool { return true })
	if count(actions, ActGiveUp, "") != 0 || count(actions, ActHello, "") != 0 {
		t.Fatalf("ya adentro no se rinde ni reintenta entrar: %+v", actions)
	}
}

func TestFailover_RefusedAfterJoiningIsIgnored(t *testing.T) {
	f := newClient("A", testRoom(false))
	f.Refused("La sala está llena.")
	if actions := f.Drain(); len(actions) != 0 {
		t.Fatalf("a refusal once in the room must be ignored, got %+v", actions)
	}
	if source := f.Seen(hostAddr, foT0); source != FromHost {
		t.Fatal("the client must still follow its host")
	}
}
