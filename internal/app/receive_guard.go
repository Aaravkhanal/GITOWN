package app

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/hex"
	"errors"
	"io"
	"strconv"
	"strings"
)

var errMalformedReceive = errors.New("malformed Git receive request")

func (a *App) protectedBranches(ctx context.Context, repositoryID string) (map[string]bool, error) {
	rows, err := a.db.Query(ctx, `SELECT branch FROM repository_branch_rules WHERE repository_id=$1 AND require_unite`, repositoryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	branches := map[string]bool{}
	for rows.Next() {
		var branch string
		if err = rows.Scan(&branch); err != nil {
			return nil, err
		}
		branches[branch] = true
	}
	return branches, rows.Err()
}

// Inspect the receive-pack command prefix, then replay the exact original
// bytes to Git. Git clients may gzip the request; the replay remains compressed.
func inspectReceiveCommands(body io.Reader, compressed bool, protected map[string]bool) (io.Reader, bool, error) {
	var original bytes.Buffer
	tee := io.TeeReader(body, &original)
	var commands io.Reader = tee
	if compressed {
		decoded, err := gzip.NewReader(tee)
		if err != nil {
			return nil, false, errMalformedReceive
		}
		defer decoded.Close()
		commands = decoded
	}
	seenCommand := false
	for count := 0; count < 1024; count++ {
		var header [4]byte
		if _, err := io.ReadFull(commands, header[:]); err != nil {
			return nil, false, errMalformedReceive
		}
		length, err := strconv.ParseUint(string(header[:]), 16, 16)
		if err != nil {
			return nil, false, errMalformedReceive
		}
		if length == 0 {
			if !seenCommand {
				return nil, false, errMalformedReceive
			}
			return io.MultiReader(bytes.NewReader(original.Bytes()), body), false, nil
		}
		if length < 4 || length > 65520 {
			return nil, false, errMalformedReceive
		}
		payload := make([]byte, int(length)-4)
		if _, err = io.ReadFull(commands, payload); err != nil {
			return nil, false, errMalformedReceive
		}
		command := string(bytes.SplitN(payload, []byte{0}, 2)[0])
		parts := strings.Fields(command)
		if len(parts) == 2 && parts[0] == "shallow" {
			continue
		}
		if len(parts) != 3 || !validObjectID(parts[0]) || !validObjectID(parts[1]) || !strings.HasPrefix(parts[2], "refs/") {
			return nil, false, errMalformedReceive
		}
		seenCommand = true
		if strings.HasPrefix(parts[2], "refs/heads/") && protected[strings.TrimPrefix(parts[2], "refs/heads/")] {
			return nil, true, nil
		}
	}
	return nil, false, errMalformedReceive
}

func validObjectID(value string) bool {
	if len(value) != 40 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
