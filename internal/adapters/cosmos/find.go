package cosmos

import "strings"

// FindContainer finds the container a connection's address points at: by
// container name, network alias or address. Otherwise it is the only
// container whose image looksLike says is the right program. chosen (the
// owner's pick) wins over both, and the container whose ID starts with selfID
// (Media Manager itself) is never picked. When nothing is found, the
// containers that look right are returned for the owner to choose from.
func FindContainer(containers []ContainerSummary, selfID, host, chosen string, looksLike func(image string) bool) (string, []string) {
	host = strings.ToLower(strings.TrimSpace(host))
	var lookAlikes []string
	for _, container := range containers {
		name := container.Name()
		if name == "" || (selfID != "" && strings.HasPrefix(container.ID, selfID)) {
			continue
		}
		if chosen != "" {
			if name == chosen {
				return name, nil
			}
			continue
		}
		if host != "" && strings.EqualFold(name, host) {
			return name, nil
		}
		for _, network := range container.NetworkSettings.Networks {
			if host != "" && network.IPAddress == host {
				return name, nil
			}
			for _, alias := range append(append([]string{}, network.Aliases...), network.DNSNames...) {
				if host != "" && strings.EqualFold(alias, host) {
					return name, nil
				}
			}
		}
		if looksLike != nil && looksLike(strings.ToLower(container.Image)) {
			lookAlikes = append(lookAlikes, name)
		}
	}
	if chosen == "" && len(lookAlikes) == 1 {
		return lookAlikes[0], nil
	}
	return "", lookAlikes
}
