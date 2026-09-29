package app

import (
	"bytes"
	"compress/gzip"
	"encoding/hex"
	"errors"
	"io"
	"strconv"
	"strings"
)

type refUpdate struct {
	Old string
	New string
	Ref string
}

var errMalformedReceive = errors.New("malformed Git receive request")

// Inspect the receive-pack command prefix, then replay the exact original
// bytes to Git. Git clients may gzip the request; the replay remains compressed.
func inspectReceiveCommands(body io.Reader, compressed bool, protected map[string]string) (io.Reader, string, []refUpdate, error) {
	var original bytes.Buffer
	var updates []refUpdate
	tee := io.TeeReader(body, &original)
	var commands io.Reader = tee
	if compressed {
		decoded, err := gzip.NewReader(tee)
		if err != nil {
			return nil, "", nil, errMalformedReceive
		}
		defer decoded.Close()
		commands = decoded
	}
	seenCommand := false
	for count := 0; count < 1024; count++ {
		var header [4]byte
		if _, err := io.ReadFull(commands, header[:]); err != nil {
			return nil, "", nil, errMalformedReceive
		}
		length, err := strconv.ParseUint(string(header[:]), 16, 16)
		if err != nil {
			return nil, "", nil, errMalformedReceive
		}
		if length == 0 {
			if !seenCommand {
				return nil, "", nil, errMalformedReceive
			}
			return io.MultiReader(bytes.NewReader(original.Bytes()), body), "", updates, nil
		}
		if length < 4 || length > 65520 {
			return nil, "", nil, errMalformedReceive
		}
		payload := make([]byte, int(length)-4)
		if _, err = io.ReadFull(commands, payload); err != nil {
			return nil, "", nil, errMalformedReceive
		}
		command := string(bytes.SplitN(payload, []byte{0}, 2)[0])
		parts := strings.Fields(command)
		if len(parts) == 2 && parts[0] == "shallow" {
			continue
		}
		if len(parts) != 3 || !validObjectID(parts[0]) || !validObjectID(parts[1]) || !strings.HasPrefix(parts[2], "refs/") {
			return nil, "", nil, errMalformedReceive
		}
		seenCommand = true
		updates = append(updates, refUpdate{Old: parts[0], New: parts[1], Ref: parts[2]})
		if strings.HasPrefix(parts[2], "refs/heads/") && protected[strings.TrimPrefix(parts[2], "refs/heads/")] != "" {
			return nil, strings.TrimPrefix(parts[2], "refs/heads/"), nil, nil
		}
	}
	return nil, "", nil, errMalformedReceive
}

func validObjectID(value string) bool {
	if len(value) != 40 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
