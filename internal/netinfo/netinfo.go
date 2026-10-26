package netinfo

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/huin/goupnp"
	"github.com/huin/goupnp/dcps/internetgateway1"
	"github.com/huin/goupnp/dcps/internetgateway2"
	"github.com/huin/goupnp/soap"
)

func PublicIP(ctx context.Context) (net.IP, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.ipify.org", nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("public IP service returned %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64))
	if err != nil {
		return nil, err
	}
	ip := net.ParseIP(strings.TrimSpace(string(body)))
	if ip == nil {
		return nil, fmt.Errorf("invalid public IP response")
	}
	return ip, nil
}

type portMapper interface {
	AddPortMappingCtx(context.Context, string, uint16, string, uint16, string, bool, string, uint32) error
	DeletePortMappingCtx(context.Context, string, uint16, string) error
	GetSpecificPortMappingEntryCtx(context.Context, string, uint16, string) (uint16, string, bool, string, uint32, error)
}

type clientFactory func(*goupnp.RootDevice, *url.URL) ([]portMapper, error)

// upnpConflict es ConflictInMappingEntry: el puerto externo ya tiene una regla.
const upnpConflict = 718

// Mapping es la regla UPnP que abrió MapUDP. Guarda el cliente del router que la aceptó: borrarla
// es un solo pedido, sin volver a buscar el router (eso tarda segundos y no entraba en el cierre).
type Mapping struct {
	External uint16
	mapper   portMapper
}

// Unmap borra la regla del router.
func (m *Mapping) Unmap(ctx context.Context) error {
	return m.mapper.DeletePortMappingCtx(ctx, "", m.External, "UDP")
}

// MapUDP abre por UPnP el primer puerto externo de candidates que el router acepte, siempre hacia
// internalPort de esta PC (donde escucha el socket), y devuelve la regla que quedó. Un puerto
// externo ocupado por otra regla (ConflictInMappingEntry) pasa al siguiente candidato, salvo que
// la regla sea de esta PC y este puerto (ver addMapping). lifetime es el lease en segundos: al
// vencer, el router la borra. Sin UPnP en el router devuelve error; se puede abrir el puerto a mano.
func MapUDP(ctx context.Context, candidates []uint16, internalPort uint16, lifetime uint32) (*Mapping, error) {
	attempts := []struct {
		name    string
		urn     string
		clients clientFactory
	}{
		{
			name: "WANIPConnection v1",
			urn:  internetgateway1.URN_WANIPConnection_1,
			clients: func(root *goupnp.RootDevice, location *url.URL) ([]portMapper, error) {
				clients, err := internetgateway1.NewWANIPConnection1ClientsFromRootDevice(root, location)
				return asPortMappers(clients), err
			},
		},
		{
			name: "WANPPPConnection v1",
			urn:  internetgateway1.URN_WANPPPConnection_1,
			clients: func(root *goupnp.RootDevice, location *url.URL) ([]portMapper, error) {
				clients, err := internetgateway1.NewWANPPPConnection1ClientsFromRootDevice(root, location)
				return asPortMappers(clients), err
			},
		},
		{
			name: "WANIPConnection v2",
			urn:  internetgateway2.URN_WANIPConnection_2,
			clients: func(root *goupnp.RootDevice, location *url.URL) ([]portMapper, error) {
				clients, err := internetgateway2.NewWANIPConnection2ClientsFromRootDevice(root, location)
				return asPortMappers(clients), err
			},
		},
	}

	var failures []error
	for _, attempt := range attempts {
		devices, err := goupnp.DiscoverDevicesCtx(ctx, attempt.urn)
		if err != nil {
			failures = append(failures, fmt.Errorf("%s discovery: %w", attempt.name, err))
			continue
		}
		if len(devices) == 0 {
			failures = append(failures, fmt.Errorf("%s: no gateway found", attempt.name))
			continue
		}
		mapping, err := mapDevices(ctx, candidates, internalPort, lifetime, attempt.name, devices, attempt.clients)
		if err == nil {
			return mapping, nil
		}
		failures = append(failures, err)
	}
	return nil, fmt.Errorf("UPnP UDP port mapping failed: %w", errors.Join(failures...))
}

func mapDevices(ctx context.Context, candidates []uint16, internalPort uint16, lifetime uint32, name string, devices []goupnp.MaybeRootDevice, clients clientFactory) (*Mapping, error) {
	var failures []error
	for _, device := range devices {
		if device.Err != nil {
			failures = append(failures, fmt.Errorf("%s device: %w", name, device.Err))
			continue
		}
		if device.Root == nil || device.Location == nil {
			failures = append(failures, fmt.Errorf("%s device has incomplete discovery data", name))
			continue
		}
		localIP := device.LocalAddr.To4()
		if localIP == nil {
			var err error
			localIP, err = LocalIPv4()
			if err != nil {
				failures = append(failures, fmt.Errorf("%s local address: %w", name, err))
				continue
			}
		}
		mappers, err := clients(device.Root, device.Location)
		if err != nil {
			failures = append(failures, fmt.Errorf("%s client: %w", name, err))
			continue
		}
		if len(mappers) == 0 {
			failures = append(failures, fmt.Errorf("%s has no available clients", name))
			continue
		}
		for _, mapper := range mappers {
			for _, external := range candidates {
				err = addMapping(ctx, mapper, external, internalPort, localIP.String(), lifetime)
				if err == nil {
					return &Mapping{External: external, mapper: mapper}, nil
				}
				failures = append(failures, fmt.Errorf("%s mapping %d to %s:%d: %w", name, external, localIP, internalPort, err))
				if ctx.Err() != nil {
					return nil, errors.Join(failures...)
				}
			}
		}
	}
	return nil, errors.Join(failures...)
}

// addMapping agrega la regla external → localIP:internalPort. Si el puerto ya tiene una regla hacia
// esta misma PC y puerto, es de una sala que no la borró (cuelgue, cierre forzado): se rehace en el
// mismo puerto, así la dirección que tienen los demás sigue sirviendo. Algunos routers responden
// ConflictInMappingEntry aunque la regla sea la misma; sin esto se saltaba al puerto siguiente.
func addMapping(ctx context.Context, mapper portMapper, external, internalPort uint16, localIP string, lifetime uint32) error {
	err := mapper.AddPortMappingCtx(ctx, "", external, "UDP", internalPort, localIP, true, "VoxMesh", lifetime)
	if err == nil || !isUPnPError(err, upnpConflict) {
		return err
	}
	port, client, _, _, _, getErr := mapper.GetSpecificPortMappingEntryCtx(ctx, "", external, "UDP")
	if getErr != nil || port != internalPort || client != localIP {
		return err // la regla es de otra PC o de otro puerto: no se toca
	}
	if err := mapper.DeletePortMappingCtx(ctx, "", external, "UDP"); err != nil {
		return fmt.Errorf("borrar la regla vieja de esta PC: %w", err)
	}
	return mapper.AddPortMappingCtx(ctx, "", external, "UDP", internalPort, localIP, true, "VoxMesh", lifetime)
}

func isUPnPError(err error, code int) bool {
	var fault *soap.SOAPFaultError
	return errors.As(err, &fault) && fault.Detail.UPnPError.Errorcode == code
}

func asPortMappers[T portMapper](clients []T) []portMapper {
	mappers := make([]portMapper, len(clients))
	for index, client := range clients {
		mappers[index] = client
	}
	return mappers
}

func LocalIPv4() (net.IP, error) {
	conn, err := net.Dial("udp", "1.1.1.1:53")
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	addr, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok || addr.IP.To4() == nil {
		return nil, fmt.Errorf("no local IPv4 address")
	}
	return addr.IP.To4(), nil
}
