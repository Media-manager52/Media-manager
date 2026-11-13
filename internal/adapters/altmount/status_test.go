package altmount

import (
	"context"
	"io"
	"net/http"
	"testing"
)

func TestStreamsIsEmptyWhenNothingPlays(t *testing.T) {
	body := `{"data":null,"success":true}`
	client, _ := fakeProviderAltMount(t, func(writer http.ResponseWriter, request providerRequest) {
		_, _ = io.WriteString(writer, body)
	})
	streams, err := client.Streams(context.Background())
	if err != nil || streams == nil || len(streams) != 0 {
		t.Fatalf("streams=%v err=%v", streams, err)
	}
	body = `{"data":[{"file_path":"/complete/a.mkv","total_size":2000,"current_offset":500,"status":"Streaming"}],"success":true}`
	streams, err = client.Streams(context.Background())
	if err != nil || len(streams) != 1 || streams[0].FilePath != "/complete/a.mkv" || streams[0].CurrentOffset != 500 {
		t.Fatalf("streams=%+v err=%v", streams, err)
	}
	body = `{"data":null,"success":false}`
	if _, err := client.Streams(context.Background()); err == nil {
		t.Fatal("a refused request must stay an error")
	}
}
