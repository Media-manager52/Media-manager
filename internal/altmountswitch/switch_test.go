package altmountswitch

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/media-manager/media-manager/internal/adapters/cosmos"
)

type fakeCosmos struct {
	containers []cosmos.ContainerSummary
	image      string
	memory     int64
	running    bool
	updates    []string
	mounts     []cosmos.Mount
	binds      string
	updateErr  error
}

func (fake *fakeCosmos) Containers(context.Context) ([]cosmos.ContainerSummary, error) {
	return fake.containers, nil
}

func (fake *fakeCosmos) Container(_ context.Context, name string) (cosmos.Container, error) {
	var container cosmos.Container
	raw := `{"Name":"/` + name + `","Config":{"Image":"` + fake.image + `"},"State":{"Running":true,"Status":"running"},"HostConfig":{"Memory":536870912,"Binds":[` + fake.binds + `]},"Mounts":[{"Type":"volume","Name":"0a1b2c","Destination":"/metadata","RW":true}]}`
	if err := json.Unmarshal([]byte(raw), &container); err != nil {
		return container, err
	}
	container.State.Running = fake.running
	return container, nil
}

func (fake *fakeCosmos) UpdateImage(_ context.Context, name, image string, mounts []cosmos.Mount, memory, _ int64) error {
	if fake.updateErr != nil {
		return fake.updateErr
	}
	fake.updates = append(fake.updates, name+"="+image)
	fake.image, fake.memory, fake.mounts = image, memory, mounts
	return nil
}

type fakeAltMount struct{ version func() (string, error) }

func (fake fakeAltMount) Version(context.Context) (string, error) { return fake.version() }

func summary(name, image, alias string) cosmos.ContainerSummary {
	var container cosmos.ContainerSummary
	raw := `{"Id":"` + name + `-id","Names":["/` + name + `"],"Image":"` + image + `","NetworkSettings":{"Networks":{"media":{"IPAddress":"172.20.0.9","Aliases":["` + alias + `"]}}}}`
	if err := json.Unmarshal([]byte(raw), &container); err != nil {
		panic(err)
	}
	return container
}

// versionByImage answers like the AltMount the fake container runs.
func versionByImage(fake *fakeCosmos, standard string) func() (string, error) {
	return func() (string, error) {
		if fake.image == Image {
			return "v0.4.0-mm.3", nil
		}
		return standard, nil
	}
}

func newSwitcher(fake *fakeCosmos, version func() (string, error)) Switcher {
	return Switcher{
		Cosmos:       fake,
		AltMount:     func(context.Context) (AltMount, error) { return fakeAltMount{version: version}, nil },
		Sleep:        func(context.Context, time.Duration) error { return nil },
		StartTimeout: time.Millisecond,
	}
}

func TestKindAndCanSwitch(t *testing.T) {
	cases := []struct {
		version string
		kind    string
		can     bool
	}{
		{"v0.4.0", KindStandard, true},
		{"0.4.0-amd64", KindStandard, true},
		{"v0.3.2", KindStandard, true},
		{"v0.4.1", KindStandard, false},
		{"v1.0.0", KindStandard, false},
		{"v0.4.0-mm.3", KindMediaManager, false},
		{"mm-3ef0f6e", KindMediaManager, false},
		{"dev", KindUnknown, false},
		{"", KindUnknown, false},
	}
	for _, test := range cases {
		if kind := Kind(test.version); kind != test.kind {
			t.Errorf("Kind(%q) = %s, want %s", test.version, kind, test.kind)
		}
		if can := CanSwitch(test.version); can != test.can {
			t.Errorf("CanSwitch(%q) = %v, want %v", test.version, can, test.can)
		}
	}
}

func TestPreviewChangesNothing(t *testing.T) {
	fake := &fakeCosmos{containers: []cosmos.ContainerSummary{summary("mm-api", "media-manager-api", "api"), summary("altmount", "ghcr.io/kipsilabs/altmount:latest", "altmount")}, image: "ghcr.io/kipsilabs/altmount:latest", running: true}
	plan, err := newSwitcher(fake, versionByImage(fake, "v0.4.0")).Run(context.Background(), Request{Self: "mm-api", AltMountHost: "altmount"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Container != "altmount" || plan.CurrentImage != "ghcr.io/kipsilabs/altmount:latest" || plan.CurrentVersion != "v0.4.0" || plan.NewImage != Image || plan.Applied || len(fake.updates) != 0 {
		t.Fatalf("plan = %+v, updates = %v", plan, fake.updates)
	}
}

func TestSwitchKeepsLimitsAndWaitsForTheNewBuild(t *testing.T) {
	fake := &fakeCosmos{containers: []cosmos.ContainerSummary{summary("altmount", "ghcr.io/kipsilabs/altmount:latest", "usenet")}, image: "ghcr.io/kipsilabs/altmount:latest", running: true, binds: `"/srv/altmount/config:/config"`}
	plan, err := newSwitcher(fake, versionByImage(fake, "v0.4.0")).Run(context.Background(), Request{AltMountHost: "172.20.0.9", Apply: true})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Applied || plan.NewVersion != "v0.4.0-mm.3" || len(fake.updates) != 1 || fake.updates[0] != "altmount="+Image || fake.memory != 536870912 {
		t.Fatalf("plan = %+v, updates = %v, memory = %d", plan, fake.updates, fake.memory)
	}
	sent, _ := json.Marshal(fake.mounts)
	if string(sent) != `[{"Source":"/srv/altmount/config","Target":"/config","Type":"bind"},{"Source":"0a1b2c","Target":"/metadata","Type":"volume"}]` {
		t.Fatalf("mounts sent: %s", sent)
	}
}

func TestSwitchPutsTheOldImageBackWhenTheNewOneDoesNotAnswer(t *testing.T) {
	fake := &fakeCosmos{containers: []cosmos.ContainerSummary{summary("altmount", "ghcr.io/kipsilabs/altmount:v0.4.0", "altmount")}, image: "ghcr.io/kipsilabs/altmount:v0.4.0", running: true}
	switcher := newSwitcher(fake, func() (string, error) {
		if fake.image == Image {
			return "", errors.New("connection refused")
		}
		return "v0.4.0", nil
	})
	_, err := switcher.Run(context.Background(), Request{AltMountHost: "altmount", Apply: true})
	var rolledBack RolledBackError
	if !errors.As(err, &rolledBack) || rolledBack.RollbackErr != nil {
		t.Fatalf("err = %v", err)
	}
	if len(fake.updates) != 2 || fake.updates[1] != "altmount=ghcr.io/kipsilabs/altmount:v0.4.0" || fake.image != "ghcr.io/kipsilabs/altmount:v0.4.0" {
		t.Fatalf("updates = %v", fake.updates)
	}
}

func TestNewerOrUnknownAltMountIsNotSwitched(t *testing.T) {
	for _, version := range []string{"v0.5.0", "dev"} {
		fake := &fakeCosmos{containers: []cosmos.ContainerSummary{summary("altmount", "ghcr.io/kipsilabs/altmount:latest", "altmount")}, image: "ghcr.io/kipsilabs/altmount:latest", running: true}
		_, err := newSwitcher(fake, versionByImage(fake, version)).Run(context.Background(), Request{AltMountHost: "altmount", Apply: true})
		var versionErr VersionError
		if !errors.As(err, &versionErr) || versionErr.Newer != (version == "v0.5.0") || len(fake.updates) != 0 {
			t.Fatalf("%s: err = %v, updates = %v", version, err, fake.updates)
		}
	}
}

func TestMediaManagersBuildIsLeftAlone(t *testing.T) {
	fake := &fakeCosmos{containers: []cosmos.ContainerSummary{summary("mm-altmount", Image, "altmount")}, image: Image, running: true}
	plan, err := newSwitcher(fake, versionByImage(fake, "")).Run(context.Background(), Request{AltMountHost: "altmount", Apply: true})
	if err != nil || !plan.AlreadyMediaManager || len(fake.updates) != 0 {
		t.Fatalf("plan = %+v, err = %v", plan, err)
	}
}

func TestSeveralAltMountsAreChosenFrom(t *testing.T) {
	fake := &fakeCosmos{containers: []cosmos.ContainerSummary{summary("altmount-a", "ghcr.io/kipsilabs/altmount:latest", "a"), summary("altmount-b", "ghcr.io/kipsilabs/altmount:latest", "b")}, image: "ghcr.io/kipsilabs/altmount:latest", running: true}
	switcher := newSwitcher(fake, versionByImage(fake, "v0.4.0"))
	plan, err := switcher.Run(context.Background(), Request{AltMountHost: "nas.local"})
	if !errors.Is(err, ErrChooseAltMount) || len(plan.Candidates) != 2 {
		t.Fatalf("plan = %+v, err = %v", plan, err)
	}
	plan, err = switcher.Run(context.Background(), Request{AltMountHost: "nas.local", Container: "altmount-b"})
	if err != nil || plan.Container != "altmount-b" {
		t.Fatalf("plan = %+v, err = %v", plan, err)
	}
}

func TestAFolderCosmosCannotKeepStopsTheSwitch(t *testing.T) {
	fake := &fakeCosmos{containers: []cosmos.ContainerSummary{summary("altmount", "ghcr.io/kipsilabs/altmount:latest", "altmount")}, image: "ghcr.io/kipsilabs/altmount:latest", running: true, binds: `"/srv/altmount/config:/config:Z"`}
	_, err := newSwitcher(fake, versionByImage(fake, "v0.4.0")).Run(context.Background(), Request{AltMountHost: "altmount", Apply: true})
	if !errors.Is(err, ErrUnsupportedFolder) || len(fake.updates) != 0 {
		t.Fatalf("err = %v, updates = %v", err, fake.updates)
	}
}
