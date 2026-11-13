package cosmos

import (
	"encoding/json"
	"errors"
	"path"
	"strings"
)

// Mount is one entry of Cosmos' update form, Docker's mount description.
// Fields Media Manager does not know are sent back exactly as they were.
type Mount struct {
	Type        string
	Source      string
	Target      string
	ReadOnly    bool
	BindOptions map[string]any
	other       map[string]json.RawMessage
}

// UnmarshalJSON keeps every field.
func (mount *Mount) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	decode := func(name string, target any) error {
		raw, ok := fields[name]
		if !ok || string(raw) == "null" {
			return nil
		}
		delete(fields, name)
		return json.Unmarshal(raw, target)
	}
	for name, target := range map[string]any{"Type": &mount.Type, "Source": &mount.Source, "Target": &mount.Target, "ReadOnly": &mount.ReadOnly, "BindOptions": &mount.BindOptions} {
		if err := decode(name, target); err != nil {
			return err
		}
	}
	delete(fields, "BindOptions")
	mount.other = fields
	return nil
}

// MarshalJSON writes the known fields over the kept ones.
func (mount Mount) MarshalJSON() ([]byte, error) {
	fields := map[string]any{}
	for name, raw := range mount.other {
		fields[name] = raw
	}
	fields["Type"], fields["Source"], fields["Target"] = mount.Type, mount.Source, mount.Target
	if mount.ReadOnly {
		fields["ReadOnly"] = true
	}
	if len(mount.BindOptions) > 0 {
		fields["BindOptions"] = mount.BindOptions
	}
	return json.Marshal(fields)
}

// Propagation is the bind's mount propagation, "" for Docker's default
// (rprivate).
func (mount Mount) Propagation() string {
	value, _ := mount.BindOptions["Propagation"].(string)
	return value
}

// SetPropagation sets the bind's mount propagation.
func (mount *Mount) SetPropagation(value string) {
	if mount.BindOptions == nil {
		mount.BindOptions = map[string]any{}
	}
	mount.BindOptions["Propagation"] = value
}

// ErrUnsupportedBind means a folder is added with an option Cosmos' update
// form cannot keep, so the container is left alone.
var ErrUnsupportedBind = errors.New("a folder uses an option Media Manager cannot keep")

// MountList is every folder and volume the container has, in the form
// Cosmos' update route takes: its mounts, its "-v" style binds, and volumes
// the image created itself, which would otherwise be replaced by new empty
// ones when the container is recreated.
func (container Container) MountList() ([]Mount, error) {
	mounts := make([]Mount, 0, len(container.HostConfig.Mounts)+len(container.HostConfig.Binds))
	targets := map[string]bool{}
	for _, raw := range container.HostConfig.Mounts {
		var mount Mount
		if err := json.Unmarshal(raw, &mount); err != nil {
			return nil, err
		}
		mounts = append(mounts, mount)
		targets[path.Clean(mount.Target)] = true
	}
	for _, bind := range container.HostConfig.Binds {
		mount, err := parseBind(bind)
		if err != nil {
			return nil, err
		}
		mounts = append(mounts, mount)
		targets[path.Clean(mount.Target)] = true
	}
	for _, point := range container.Mounts {
		if point.Type == "volume" && point.Name != "" && !targets[path.Clean(point.Destination)] {
			mounts = append(mounts, Mount{Type: "volume", Source: point.Name, Target: point.Destination, ReadOnly: !point.RW})
			targets[path.Clean(point.Destination)] = true
		}
	}
	return mounts, nil
}

var propagations = map[string]bool{"private": true, "rprivate": true, "shared": true, "rshared": true, "slave": true, "rslave": true}

// parseBind reads a "-v source:target[:options]" bind.
func parseBind(bind string) (Mount, error) {
	parts := strings.Split(bind, ":")
	if len(parts) < 2 || len(parts) > 3 || parts[0] == "" || !strings.HasPrefix(parts[1], "/") {
		return Mount{}, ErrUnsupportedBind
	}
	mount := Mount{Type: "bind", Source: parts[0], Target: parts[1]}
	if !strings.HasPrefix(parts[0], "/") {
		mount.Type = "volume"
	}
	if len(parts) == 3 {
		for _, option := range strings.Split(parts[2], ",") {
			switch {
			case option == "ro":
				mount.ReadOnly = true
			case option == "rw" || option == "" || option == "consistent" || option == "cached" || option == "delegated":
			case propagations[option] && mount.Type == "bind":
				mount.SetPropagation(option)
			default:
				return Mount{}, ErrUnsupportedBind
			}
		}
	}
	return mount, nil
}
