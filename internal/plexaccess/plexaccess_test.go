package plexaccess

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/media-manager/media-manager/internal/adapters/cosmos"
	"github.com/media-manager/media-manager/internal/adapters/plex"
)

type fakeCosmos struct {
	containers map[string]cosmos.Container
	list       []cosmos.ContainerSummary
	updated    []cosmos.Mount
	memory     int64
	cpus       int64
	updates    int
	// unhealthy is how many reads of Plex say it is still starting.
	unhealthy int
}

func (fake *fakeCosmos) Containers(context.Context) ([]cosmos.ContainerSummary, error) {
	return fake.list, nil
}

func (fake *fakeCosmos) Container(_ context.Context, name string) (cosmos.Container, error) {
	container, ok := fake.containers[name]
	if !ok {
		return container, errors.New("no such container")
	}
	if fake.updates > 0 && fake.unhealthy > 0 {
		fake.unhealthy--
		container.State.Running = false
	}
	return container, nil
}

func (fake *fakeCosmos) UpdateMounts(_ context.Context, name string, mounts []cosmos.Mount, memory, cpus int64) error {
	fake.updates++
	fake.updated, fake.memory, fake.cpus = mounts, memory, cpus
	return nil
}

type fakePlex struct {
	sections []plex.Section
	created  []string
	added    []string
}

func (fake *fakePlex) Sections(context.Context) ([]plex.Section, error) { return fake.sections, nil }
func (fake *fakePlex) CreateSection(_ context.Context, kind, name, language, folder string) error {
	fake.created = append(fake.created, kind+" "+name+" "+language+" "+folder)
	return nil
}
func (fake *fakePlex) AddLocation(_ context.Context, section plex.Section, folder string) error {
	paths := []string{}
	for _, location := range section.Locations {
		paths = append(paths, location.Path)
	}
	fake.added = append(fake.added, section.Title+" "+folder+" "+jsonText(paths))
	return nil
}

func jsonText(value any) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func container(id, name string, points []cosmos.MountPoint, binds []string, mounts ...string) cosmos.Container {
	var result cosmos.Container
	result.ID, result.Name = id, "/"+name
	result.State.Running = true
	result.Mounts = points
	result.HostConfig.Binds = binds
	for _, mount := range mounts {
		result.HostConfig.Mounts = append(result.HostConfig.Mounts, json.RawMessage(mount))
	}
	return result
}

func summary(id, name, image string) cosmos.ContainerSummary {
	return cosmos.ContainerSummary{ID: id, Names: []string{"/" + name}, Image: image}
}

func selfContainer() cosmos.Container {
	return container("aaa111", "mm-api", []cosmos.MountPoint{
		{Type: "bind", Source: "/srv/mm/usenet", Destination: "/mnt", Propagation: "rslave"},
		{Type: "bind", Source: "/srv/mm/movies", Destination: "/libraries/movies", RW: true},
		{Type: "bind", Source: "/srv/mm/tv", Destination: "/libraries/tv", RW: true},
		{Type: "volume", Name: "mm-api-state", Source: "/var/lib/docker/volumes/mm-api-state/_data", Destination: "/var/lib/media-manager/state", RW: true},
	}, nil)
}

var allNeeds = []Need{{PurposeAltMount, "/mnt/altmount"}, {PurposeMovies, "/libraries/movies"}, {PurposeTV, "/libraries/tv"}}

func TestFreshPlexGetsEveryFolderAndLibraries(t *testing.T) {
	plexContainer := container("bbb222", "plex", []cosmos.MountPoint{
		{Type: "bind", Source: "/srv/movies", Destination: "/data/movies", RW: true, Propagation: "rprivate"},
		{Type: "volume", Name: "plex-config", Destination: "/config", RW: true},
		{Type: "volume", Name: "0f3c", Destination: "/transcode", RW: true}, // made by the image
	}, []string{"/srv/movies:/data/movies"}, `{"Type":"volume","Source":"plex-config","Target":"/config","VolumeOptions":{"NoCopy":true}}`)
	plexContainer.HostConfig.Memory = 4 << 30
	plexContainer.HostConfig.NanoCpus = 2e9
	fake := &fakeCosmos{
		containers: map[string]cosmos.Container{"aaa111": selfContainer(), "plex": plexContainer},
		list:       []cosmos.ContainerSummary{summary("aaa111def", "mm-api", "media-manager-api"), summary("bbb222", "plex", "lscr.io/linuxserver/plex:latest"), summary("ccc", "seerr", "seerr")},
		unhealthy:  2,
	}
	plexAPI := &fakePlex{sections: []plex.Section{{Key: "1", Title: "Film", Type: "movie", Agent: "tv.plex.agents.movie", Language: "da-DK", Locations: []plex.Location{{Path: "/data/movies"}}}}}
	slept := 0
	fixer := Fixer{Cosmos: fake, Plex: func(context.Context) (Plex, error) { return plexAPI, nil }, Sleep: func(context.Context, time.Duration) error { slept++; return nil }}

	plan, err := fixer.Run(context.Background(), Request{Self: "aaa111", PlexHost: "192.168.1.5", Needs: allNeeds})
	if err != nil {
		t.Fatal(err)
	}
	wantChanges := []Change{
		{Kind: ChangeAddFolder, Purpose: PurposeAltMount, Path: "/mnt/altmount"},
		{Kind: ChangeAddFolder, Purpose: PurposeMovies, Path: "/libraries/movies"},
		{Kind: ChangeAddFolder, Purpose: PurposeTV, Path: "/libraries/tv"},
		{Kind: ChangeAddLibraryPath, Purpose: PurposeMovies, Path: "/libraries/movies", Library: "Film"},
		{Kind: ChangeCreateLibrary, Purpose: PurposeTV, Path: "/libraries/tv", Library: "Serier"},
	}
	if plan.PlexContainer != "plex" || !plan.Restart || plan.Applied || !reflect.DeepEqual(plan.Changes, wantChanges) {
		t.Fatalf("plan = %+v", plan)
	}
	if fake.updates != 0 || len(plexAPI.added)+len(plexAPI.created) != 0 {
		t.Fatal("a preview changed something")
	}

	plan, err = fixer.Run(context.Background(), Request{Self: "aaa111", PlexHost: "192.168.1.5", Needs: allNeeds, Apply: true})
	if err != nil || !plan.Applied {
		t.Fatalf("apply: %+v %v", plan, err)
	}
	got := jsonText(fake.updated)
	want := `[{"Source":"plex-config","Target":"/config","Type":"volume","VolumeOptions":{"NoCopy":true}},` +
		`{"Source":"/srv/movies","Target":"/data/movies","Type":"bind"},` +
		`{"Source":"0f3c","Target":"/transcode","Type":"volume"},` +
		`{"BindOptions":{"Propagation":"rslave"},"ReadOnly":true,"Source":"/srv/mm/usenet","Target":"/mnt","Type":"bind"},` +
		`{"ReadOnly":true,"Source":"/srv/mm/movies","Target":"/libraries/movies","Type":"bind"},` +
		`{"ReadOnly":true,"Source":"/srv/mm/tv","Target":"/libraries/tv","Type":"bind"}]`
	if got != want {
		t.Fatalf("mounts sent to Cosmos:\n%s\nwant\n%s", got, want)
	}
	if fake.memory != 4<<30 || fake.cpus != 2e9 || slept != 2 {
		t.Fatalf("limits %d %d, slept %d", fake.memory, fake.cpus, slept)
	}
	if !reflect.DeepEqual(plexAPI.added, []string{`Film /libraries/movies ["/data/movies"]`}) || !reflect.DeepEqual(plexAPI.created, []string{"show Serier da-DK /libraries/tv"}) {
		t.Fatalf("library changes: %v %v", plexAPI.added, plexAPI.created)
	}
}

func TestPlexThatHasTheDriveOnlyGetsPropagation(t *testing.T) {
	for _, test := range []struct {
		name        string
		propagation string
		changes     []Change
	}{
		{"rprivate", "rprivate", []Change{{Kind: ChangeSetPropagation, Purpose: PurposeAltMount, Path: "/data/usenet/altmount"}}},
		{"rshared", "rshared", []Change{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Plex has a folder holding Media Manager's AltMount folder.
			plexContainer := container("bbb", "plex", []cosmos.MountPoint{{Type: "bind", Source: "/srv/mm", Destination: "/data", RW: true, Propagation: test.propagation}}, []string{"/srv/mm:/data:ro," + test.propagation})
			fake := &fakeCosmos{containers: map[string]cosmos.Container{"aaa111": selfContainer(), "plex": plexContainer}, list: []cosmos.ContainerSummary{summary("bbb", "plex", "plexinc/pms-docker")}}
			fixer := Fixer{Cosmos: fake, Plex: func(context.Context) (Plex, error) { return nil, errors.New("not needed") }, Sleep: func(context.Context, time.Duration) error { return nil }}
			plan, err := fixer.Run(context.Background(), Request{Self: "aaa111", PlexHost: "plex", Needs: allNeeds[:1], Apply: true})
			if err != nil || !reflect.DeepEqual(plan.Changes, test.changes) || plan.PlexPaths[PurposeAltMount] != "/data/usenet/altmount" {
				t.Fatalf("plan = %+v, %v", plan, err)
			}
			if test.propagation == "rprivate" && jsonText(fake.updated) != `[{"BindOptions":{"Propagation":"rslave"},"ReadOnly":true,"Source":"/srv/mm","Target":"/data","Type":"bind"}]` {
				t.Fatalf("mounts = %s", jsonText(fake.updated))
			}
			if test.propagation == "rshared" && fake.updates != 0 {
				t.Fatal("Plex was recreated without a change")
			}
		})
	}
}

func TestBusyPlaceAndChoosingPlex(t *testing.T) {
	// Plex uses /mnt for another folder: Media Manager's goes below /media-manager.
	plexContainer := container("bbb", "plex", []cosmos.MountPoint{{Type: "bind", Source: "/mnt", Destination: "/mnt", RW: true, Propagation: "rshared"}}, []string{"/mnt:/mnt:rshared"})
	fake := &fakeCosmos{containers: map[string]cosmos.Container{"aaa111": selfContainer(), "plex": plexContainer, "plex2": plexContainer}, list: []cosmos.ContainerSummary{summary("bbb", "plex", "plexinc/pms-docker"), summary("ddd", "plex2", "lscr.io/linuxserver/plex"), summary("eee", "kometa", "kometateam/kometa")}}
	fixer := Fixer{Cosmos: fake, Plex: func(context.Context) (Plex, error) { return nil, errors.New("not needed") }}
	plan, err := fixer.Run(context.Background(), Request{Self: "aaa111", PlexHost: "plex.example.test", Needs: allNeeds[:1]})
	if !errors.Is(err, ErrChoosePlex) || !reflect.DeepEqual(plan.Candidates, []string{"plex", "plex2"}) {
		t.Fatalf("plan = %+v, %v", plan, err)
	}
	plan, err = fixer.Run(context.Background(), Request{Self: "aaa111", PlexHost: "plex.example.test", PlexContainer: "plex", Needs: allNeeds[:1]})
	if err != nil || plan.PlexPaths[PurposeAltMount] != "/media-manager/mnt/altmount" || plan.Changes[0].Kind != ChangeAddFolder {
		t.Fatalf("plan = %+v, %v", plan, err)
	}
	// A plex.direct address names the container by its address.
	direct := cosmos.ContainerSummary{ID: "fff", Names: []string{"/media-server"}, Image: "custom"}
	direct.NetworkSettings.Networks = map[string]cosmos.Network{"media": {IPAddress: "172.18.0.6"}}
	if name, _ := findPlex([]cosmos.ContainerSummary{direct}, "", "172-18-0-6.abc123.plex.direct", ""); name != "media-server" {
		t.Fatalf("plex.direct match = %q", name)
	}
	if _, err := (Fixer{Cosmos: &fakeCosmos{containers: map[string]cosmos.Container{"aaa111": selfContainer()}}}).Run(context.Background(), Request{Self: "aaa111"}); !errors.Is(err, ErrPlexNotFound) {
		t.Fatalf("no Plex: %v", err)
	}
}

func TestBindsCosmosCannotKeepLeavePlexAlone(t *testing.T) {
	plexContainer := container("bbb", "plex", nil, []string{"/srv/movies:/data/movies:ro,z"})
	fake := &fakeCosmos{containers: map[string]cosmos.Container{"aaa111": selfContainer(), "plex": plexContainer}, list: []cosmos.ContainerSummary{summary("bbb", "plex", "plex")}}
	if _, err := (Fixer{Cosmos: fake}).Run(context.Background(), Request{Self: "aaa111", Needs: allNeeds[:1], Apply: true}); !errors.Is(err, cosmos.ErrUnsupportedBind) || fake.updates != 0 {
		t.Fatalf("err = %v, updates = %d", err, fake.updates)
	}
}
