package app

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"strings"
	"testing"
)

func receivePacket(payload string) string {
	return fmt.Sprintf("%04x%s", len(payload)+4, payload)
}

func TestInspectReceiveCommands(t *testing.T) {
	zero := strings.Repeat("0", 40)
	sha := strings.Repeat("a", 40)
	protected := map[string]string{"main": "unite"}
	makeBody := func(branch string) []byte {
		return []byte(receivePacket(zero+" "+sha+" refs/heads/"+branch+"\x00report-status\n") + "0000PACKdata")
	}
	for _, tc := range []struct {
		name      string
		body      []byte
		gzip      bool
		blocked   bool
		malformed bool
	}{
		{name: "protected branch", body: makeBody("main"), blocked: true},
		{name: "unprotected branch", body: makeBody("feature")},
		{name: "invalid command", body: []byte(receivePacket("garbage\n") + "0000"), malformed: true},
		{name: "truncated command", body: []byte("0080short"), malformed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			replay, blocked, _, err := inspectReceiveCommands(bytes.NewReader(tc.body), tc.gzip, protected)
			if (blocked != "") != tc.blocked || (err != nil) != tc.malformed {
				t.Fatalf("blocked=%q err=%v", blocked, err)
			}
			if err == nil && blocked == "" {
				got, err := io.ReadAll(replay)
				if err != nil || !bytes.Equal(got, tc.body) {
					t.Fatalf("replayed request differs: %v", err)
				}
			}
		})
	}
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write(makeBody("feature")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	replay, blocked, _, err := inspectReceiveCommands(bytes.NewReader(compressed.Bytes()), true, protected)
	if err != nil || blocked != "" {
		t.Fatalf("gzip request: blocked=%q err=%v", blocked, err)
	}
	got, err := io.ReadAll(replay)
	if err != nil || !bytes.Equal(got, compressed.Bytes()) {
		t.Fatalf("compressed replay differs: %v", err)
	}
}
