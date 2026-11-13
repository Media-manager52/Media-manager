package cosmos

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientReadsContainersAndSendsMountsWithLimits(t *testing.T) {
	var sent map[string]any
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer token-1" {
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch request.Method + " " + request.URL.Path {
		case "GET /cosmos/api/me":
			_, _ = io.WriteString(writer, `{"status":"OK","data":{"nickname":"owner"}}`)
		case "GET /cosmos/api/servapps":
			_, _ = io.WriteString(writer, `{"status":"OK","data":[{"Id":"abc","Names":["/plex"],"Image":"lscr.io/linuxserver/plex","NetworkSettings":{"Networks":{"media":{"IPAddress":"172.18.0.6"}}}}]}`)
		case "GET /cosmos/api/servapps/plex/":
			_, _ = io.WriteString(writer, `{"status":"OK","data":{"Id":"abc","Name":"/plex","State":{"Running":true,"Health":{"Status":"healthy"}},"HostConfig":{"Binds":["/srv/config/plex:/config:rw","plex-cache:/cache","/srv/media:/media:ro,rslave"],"Mounts":[{"Type":"bind","Source":"/mnt","Target":"/mnt","BindOptions":{"Propagation":"rshared","CreateMountpoint":true}}],"Memory":1073741824},"Mounts":[{"Type":"volume","Name":"f00","Destination":"/transcode","RW":true}]}}`)
		case "POST /cosmos/api/servapps/plex/update":
			_ = json.NewDecoder(request.Body).Decode(&sent)
			_, _ = io.WriteString(writer, `{"status":"OK"}`)
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	host := strings.TrimPrefix(server.URL, "https://")
	client, err := New(Config{Host: "cosmos.example.test", DialAddress: host, APIKey: "token-1", TLS: server.Client().Transport.(*http.Transport).TLSClientConfig})
	if err != nil {
		t.Fatal(err)
	}
	// The test server's certificate names example.com, not the host asked
	// for: that is refused.
	if err := client.Me(context.Background()); err == nil || !strings.Contains(err.Error(), "certificate") {
		t.Fatalf("certificate for another name accepted: %v", err)
	}
	tlsConfig := server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	tlsConfig.ServerName = "example.com"
	client, _ = New(Config{Host: "cosmos.example.test", DialAddress: host, APIKey: "token-1", TLS: tlsConfig})
	if err := client.Me(context.Background()); err != nil {
		t.Fatal(err)
	}
	containers, err := client.Containers(context.Background())
	if err != nil || len(containers) != 1 || containers[0].Name() != "plex" || containers[0].NetworkSettings.Networks["media"].IPAddress != "172.18.0.6" {
		t.Fatalf("containers %+v %v", containers, err)
	}
	plex, err := client.Container(context.Background(), "plex")
	if err != nil || !plex.Healthy() {
		t.Fatalf("plex %+v %v", plex, err)
	}
	mounts, err := plex.MountList()
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(mounts)
	want := `[{"BindOptions":{"CreateMountpoint":true,"Propagation":"rshared"},"Source":"/mnt","Target":"/mnt","Type":"bind"},` +
		`{"Source":"/srv/config/plex","Target":"/config","Type":"bind"},` +
		`{"Source":"plex-cache","Target":"/cache","Type":"volume"},` +
		`{"BindOptions":{"Propagation":"rslave"},"ReadOnly":true,"Source":"/srv/media","Target":"/media","Type":"bind"},` +
		`{"Source":"f00","Target":"/transcode","Type":"volume"}]`
	if string(encoded) != want {
		t.Fatalf("mounts\n%s\nwant\n%s", encoded, want)
	}
	if err := client.UpdateMounts(context.Background(), "plex", mounts, plex.HostConfig.Memory, 1500000000); err != nil {
		t.Fatal(err)
	}
	if sent["memLimit"] != "1073741824" || sent["cpus"] != 1.5 || len(sent["Volumes"].([]any)) != 5 {
		t.Fatalf("sent %v", sent)
	}
	if _, err := client.Container(context.Background(), "../etc"); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("bad name: %v", err)
	}
	wrong, _ := New(Config{Host: "cosmos.example.test", DialAddress: host, APIKey: "other", TLS: tlsConfig})
	if err := wrong.Me(context.Background()); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("wrong token: %v", err)
	}
	for _, bind := range []string{"/a:/b:z", "/a", "/a:relative"} {
		if _, err := parseBind(bind); !errors.Is(err, ErrUnsupportedBind) {
			t.Fatalf("%s accepted", bind)
		}
	}
}
