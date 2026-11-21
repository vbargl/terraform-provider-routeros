package provider

import (
	"net"
	"net/netip"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

func (r *managedObject) normalize(field, value string) string {
	switch field {
	case "disabled":
		switch value {
		case "true", "yes":
			return "yes"
		case "false", "no", "":
			return "no"
		}
	case "place-before":
		return strings.ToUpper(value)
	case "mac-address":
		if mac, err := net.ParseMAC(value); err == nil {
			return strings.ToUpper(mac.String())
		}
	case "endpoint-port":
		if value == "" {
			return "0"
		}
	case "lease-time", "persistent-keepalive":
		if value == "" && field == "persistent-keepalive" {
			return "0s"
		}
		return normalizeDuration(value)
	case "src-address", "dst-address":
		if r.name == "firewall_rule" && value == "" {
			return "0.0.0.0/0"
		}
		return normalizePrefix(value)
	case "address":
		if r.name == "dhcp_network" {
			return normalizePrefix(value)
		}
	case "allowed-address":
		addresses := strings.Split(value, ",")
		for index, address := range addresses {
			addresses[index] = normalizePrefix(strings.TrimSpace(address))
		}
		slices.Sort(addresses)
		return strings.Join(addresses, ",")
	}
	return value
}

func normalizePrefix(value string) string {
	if prefix, err := netip.ParsePrefix(value); err == nil {
		return prefix.Masked().String()
	}
	if addr, err := netip.ParseAddr(value); err == nil {
		return netip.PrefixFrom(addr, addr.BitLen()).String()
	}
	return value
}

var routerDuration = regexp.MustCompile(`^(?:([0-9]{1,8})w\s*)?(?:([0-9]{1,8})d\s*)?(?:(\d{1,8}):(\d{2}):(\d{2})|((?:[0-9]+[hms]\s*)+))?$`)

func normalizeDuration(value string) string {
	if seconds, err := strconv.ParseUint(value, 10, 32); err == nil {
		return (time.Duration(seconds) * time.Second).String()
	}
	if duration, err := time.ParseDuration(value); err == nil {
		return duration.String()
	}
	trimmed := strings.TrimSpace(value)
	if match := routerDuration.FindStringSubmatch(trimmed); match != nil && trimmed != "" {
		var normalized time.Duration
		if match[6] != "" {
			suffix, err := time.ParseDuration(strings.Join(strings.Fields(match[6]), ""))
			if err != nil {
				return value
			}
			normalized = suffix
		}
		for index, unit := range []time.Duration{7 * 24 * time.Hour, 24 * time.Hour, time.Hour, time.Minute, time.Second} {
			number, _ := strconv.ParseUint(match[index+1], 10, 32)
			if number > uint64((time.Duration(1<<63-1)-normalized)/unit) {
				return value
			}
			normalized += time.Duration(number) * unit
		}
		return normalized.String()
	}
	return value
}
