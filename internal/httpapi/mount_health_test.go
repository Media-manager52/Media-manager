package httpapi

import (
	"encoding/json"
	"errors"
	"testing"
	"uuid"

	"github.com/media-manager/media-manager/internal/postgres"
)

func TestAltMountMountHealthReportsEachEnabledClient(t *testing.T) {
	component := func(kind string, enabled bool, root string) postgres.SourceStatus {
		config, _ := json.Marshal(map[string]string{"local_read_only_root": root})
		return postgres.SourceStatus{Integration: postgres.Integration{ID: uuid.NewV7(), Kind: kind, Name: kind + "-" + root, Enabled: enabled, Config: config}}
	}
	components := []postgres.SourceStatus{
		component("altmount", true, "/mnt/altmount"),
		component("altmount", true, "/mnt/dead"),
		component("altmount", false, "/mnt/disabled"),
		component("newznab", true, "/mnt/ignored"),
	}
	checked := []string{}
	mounts := altMountMountHealth(components, func(root string) error {
		checked = append(checked, root)
		if root == "/mnt/dead" {
			return errors.New("socket not connected")
		}
		return nil
	})
	if len(mounts) != 2 || len(checked) != 2 {
		t.Fatalf("mounts = %+v, checked = %v; want only the two enabled AltMount clients", mounts, checked)
	}
	if mounts[0].Path != "/mnt/altmount" || mounts[0].State != "available" || mounts[0].Detail != "" {
		t.Fatalf("healthy mount = %+v", mounts[0])
	}
	if mounts[1].Path != "/mnt/dead" || mounts[1].State != "unavailable" || mounts[1].Detail != "socket not connected" {
		t.Fatalf("dead mount = %+v", mounts[1])
	}
	if got := altMountMountHealth(nil, nil); got == nil || len(got) != 0 {
		t.Fatalf("no clients: mounts = %#v, want an empty list", got)
	}
}
