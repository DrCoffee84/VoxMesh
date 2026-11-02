package netinfo

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"testing"

	"github.com/huin/goupnp"
	"github.com/huin/goupnp/soap"
)

const testLocalIP = "192.168.1.39"

type fakeRule struct {
	internalPort uint16
	client       string
}

// fakeRouter se porta como el router del log: AddPortMapping sobre un puerto con regla da 718,
// aunque la regla sea de la misma PC.
type fakeRouter struct {
	rules   map[uint16]fakeRule
	deleted []uint16
}

func upnpFault(code int) error {
	fault := &soap.SOAPFaultError{FaultCode: "s:Client", FaultString: "UPnPError"}
	fault.Detail.UPnPError.Errorcode = code
	return fmt.Errorf("goupnp: %w", fault)
}

func (r *fakeRouter) AddPortMappingCtx(_ context.Context, _ string, external uint16, _ string, internalPort uint16, client string, _ bool, _ string, _ uint32) error {
	if _, ok := r.rules[external]; ok {
		return upnpFault(upnpConflict)
	}
	r.rules[external] = fakeRule{internalPort: internalPort, client: client}
	return nil
}

func (r *fakeRouter) DeletePortMappingCtx(_ context.Context, _ string, external uint16, _ string) error {
	if _, ok := r.rules[external]; !ok {
		return upnpFault(714) // NoSuchEntryInArray
	}
	delete(r.rules, external)
	r.deleted = append(r.deleted, external)
	return nil
}

func (r *fakeRouter) GetSpecificPortMappingEntryCtx(_ context.Context, _ string, external uint16, _ string) (uint16, string, bool, string, uint32, error) {
	rule, ok := r.rules[external]
	if !ok {
		return 0, "", false, "", 0, upnpFault(714)
	}
	return rule.internalPort, rule.client, true, "VoxMesh", 3600, nil
}

func mapOn(t *testing.T, router *fakeRouter, candidates ...uint16) *Mapping {
	t.Helper()
	devices := []goupnp.MaybeRootDevice{{
		Root:      &goupnp.RootDevice{},
		Location:  &url.URL{},
		LocalAddr: net.ParseIP(testLocalIP),
	}}
	clients := func(*goupnp.RootDevice, *url.URL) ([]portMapper, error) { return []portMapper{router}, nil }
	mapping, err := mapDevices(context.Background(), candidates, 47826, 8*3600, "fake", devices, clients)
	if err != nil {
		t.Fatalf("mapDevices: %v", err)
	}
	return mapping
}

func TestMapDevices_FreePort(t *testing.T) {
	router := &fakeRouter{rules: map[uint16]fakeRule{}}
	mapping := mapOn(t, router, 47826, 47827)
	if mapping.External != 47826 || len(router.deleted) != 0 {
		t.Fatalf("puerto %d, borradas %v; se esperaba 47826 sin borrar nada", mapping.External, router.deleted)
	}
}

// Regla que quedó de una sala de esta PC que no la borró: se rehace en el mismo puerto.
func TestMapDevices_ReusesOwnStaleRule(t *testing.T) {
	router := &fakeRouter{rules: map[uint16]fakeRule{47826: {internalPort: 47826, client: testLocalIP}}}
	mapping := mapOn(t, router, 47826, 47827)
	if mapping.External != 47826 {
		t.Fatalf("puerto %d, se esperaba el mismo 47826", mapping.External)
	}
	if len(router.deleted) != 1 || router.deleted[0] != 47826 {
		t.Fatalf("borradas %v, se esperaba borrar la regla vieja 47826", router.deleted)
	}
	if _, ok := router.rules[47827]; ok {
		t.Fatal("no tenía que abrir 47827")
	}
}

// Regla de otra PC (o de otro puerto de esta): no se toca, se pasa al siguiente puerto.
func TestMapDevices_SkipsForeignRule(t *testing.T) {
	for name, rule := range map[string]fakeRule{
		"otra PC":     {internalPort: 47826, client: "192.168.1.50"},
		"otro puerto": {internalPort: 5000, client: testLocalIP},
	} {
		t.Run(name, func(t *testing.T) {
			router := &fakeRouter{rules: map[uint16]fakeRule{47826: rule}}
			mapping := mapOn(t, router, 47826, 47827)
			if mapping.External != 47827 {
				t.Fatalf("puerto %d, se esperaba 47827", mapping.External)
			}
			if len(router.deleted) != 0 || router.rules[47826] != rule {
				t.Fatalf("tocó la regla ajena: borradas %v", router.deleted)
			}
		})
	}
}

func TestMappingUnmap(t *testing.T) {
	router := &fakeRouter{rules: map[uint16]fakeRule{47826: {internalPort: 47826, client: "192.168.1.50"}}}
	mapping := mapOn(t, router, 47826, 47827)
	if err := mapping.Unmap(context.Background()); err != nil {
		t.Fatalf("Unmap: %v", err)
	}
	if _, ok := router.rules[47827]; ok {
		t.Fatal("la regla 47827 sigue en el router")
	}
	if _, ok := router.rules[47826]; !ok {
		t.Fatal("Unmap borró la regla ajena 47826")
	}
}
