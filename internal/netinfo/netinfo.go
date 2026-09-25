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
}

type clientFactory func(*goupnp.RootDevice, *url.URL) ([]portMapper, error)

// TryMapUDP attempts UPnP port forwarding and returns whether it succeeded.
// Routers without UPnP simply return an error; hosting can continue manually.
func TryMapUDP(ctx context.Context, localPort uint16, lifetime uint32) error {
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
		if err := mapDevices(ctx, localPort, lifetime, attempt.name, devices, attempt.clients); err == nil {
			return nil
		} else {
			failures = append(failures, err)
		}
	}
	return fmt.Errorf("UPnP UDP port mapping failed: %w", errors.Join(failures...))
}

func mapDevices(ctx context.Context, localPort uint16, lifetime uint32, name string, devices []goupnp.MaybeRootDevice, clients clientFactory) error {
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
			localIP, err = localIPv4()
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
			err = mapper.AddPortMappingCtx(ctx, "", localPort, "UDP", localPort, localIP.String(), true, "VoxMesh", lifetime)
			if err == nil {
				return nil
			}
			failures = append(failures, fmt.Errorf("%s mapping to %s: %w", name, localIP, err))
		}
	}
	return errors.Join(failures...)
}

func asPortMappers[T portMapper](clients []T) []portMapper {
	mappers := make([]portMapper, len(clients))
	for index, client := range clients {
		mappers[index] = client
	}
	return mappers
}

func localIPv4() (net.IP, error) {
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
