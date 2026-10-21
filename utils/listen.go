package utils

import (
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/antoniomika/multilistener"
)

// IsListenerClosedErr reports whether an Accept error means the listener has been
// closed (normal shutdown). It covers both net.ErrClosed and the multilistener
// package's own sentinel, which is a plain errors.New value that does NOT wrap
// net.ErrClosed — so an accept loop that only checked net.ErrClosed would never
// recognise a closed multilistener and could spin or leak (B34).
func IsListenerClosedErr(err error) bool {
	return errors.Is(err, net.ErrClosed) || errors.Is(err, multilistener.ErrClosed)
}

const (
	// AddressSeparator is the separtor when providing multiple addresses.
	AddressSeparator = ","

	// NetworkSeparator is the sepator between a network and address.
	NetworkSeparator = "://"
)

// Listen uses the multilistener package to generate a net.Listener that uses multiple addresses.
func Listen(addresses string) (net.Listener, error) {
	listeners := map[string][]string{}
	addressList := strings.Split(addresses, AddressSeparator)

	for _, address := range addressList {
		addressSplit := strings.Split(address, NetworkSeparator)
		if len(addressSplit) != 2 {
			if _, ok := listeners["tcp"]; !ok {
				listeners["tcp"] = []string{}
			}
			listeners["tcp"] = append(listeners["tcp"], address)
			continue
		}

		if _, ok := listeners[addressSplit[0]]; !ok {
			listeners[addressSplit[0]] = []string{}
		}
		listeners[addressSplit[0]] = append(listeners[addressSplit[0]], addressSplit[1])
	}

	return multilistener.Listen(listeners)
}

// ParseAddress parse a list of addresses into a host, port, err split.
func ParseAddress(addresses string) (string, string, error) {
	addressList := strings.Split(addresses, AddressSeparator)
	addressSplit := strings.Split(addressList[0], NetworkSeparator)

	address := addressSplit[0]
	if len(addressList) == 2 {
		address = addressSplit[1]
	}
	return net.SplitHostPort(address)
}

// GenerateAddress generates an address string with ports set.
func GenerateAddress(addresses string, port uint32) string {
	newAddressList := []string{}
	addressList := strings.Split(addresses, AddressSeparator)

	for _, address := range addressList {
		newAddressList = append(newAddressList, fmt.Sprintf("%s:%d", address, port))
	}
	return strings.Join(newAddressList, AddressSeparator)
}
