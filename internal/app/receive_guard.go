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

type refUpdate struct {
	Old string
	New string
	Ref string
}

var errMalformedReceive = errors.New("malformed Git receive request")

// Inspect the receive-pack command prefix, then replay the exact original
// bytes to Git. Git clients may gzip the request; the replay remains compressed.
// Only the command list is read, so body may be a live stream. When the
// commands cannot be parsed the replay reader is still returned with the error.
func inspectReceiveCommands(body io.Reader, compressed bool, protected map[string]string) (io.Reader, string, []refUpdate, error) {
	var original bytes.Buffer
	var updates []refUpdate
	tee := io.TeeReader(body, &original)
	replay := func() io.Reader { return io.MultiReader(bytes.NewReader(original.Bytes()), body) }
	var commands io.Reader = tee
	if compressed {
		decoded, err := gzip.NewReader(tee)
		if err != nil {
			return replay(), "", nil, errMalformedReceive
		}
		defer decoded.Close()
		commands = decoded
	}
	seenCommand := false
	for count := 0; count < 1024; count++ {
		var header [4]byte
		if _, err := io.ReadFull(commands, header[:]); err != nil {
			return replay(), "", nil, errMalformedReceive
		}
		length, err := strconv.ParseUint(string(header[:]), 16, 16)
		if err != nil {
			return replay(), "", nil, errMalformedReceive
		}
		if length == 0 {
			if !seenCommand {
				return replay(), "", nil, errMalformedReceive
			}
			return replay(), "", updates, nil
		}
		if length < 4 || length > 65520 {
			return replay(), "", nil, errMalformedReceive
		}
		payload := make([]byte, int(length)-4)
		if _, err = io.ReadFull(commands, payload); err != nil {
			return replay(), "", nil, errMalformedReceive
		}
		command := string(bytes.SplitN(payload, []byte{0}, 2)[0])
		parts := strings.Fields(command)
		if len(parts) == 2 && parts[0] == "shallow" {
			continue
		}
		if len(parts) != 3 || !validObjectID(parts[0]) || !validObjectID(parts[1]) || !strings.HasPrefix(parts[2], "refs/") {
			return replay(), "", nil, errMalformedReceive
		}
		seenCommand = true
		updates = append(updates, refUpdate{Old: parts[0], New: parts[1], Ref: parts[2]})
		if strings.HasPrefix(parts[2], "refs/heads/") && protected[strings.TrimPrefix(parts[2], "refs/heads/")] != "" {
			return nil, strings.TrimPrefix(parts[2], "refs/heads/"), nil, nil
		}
	}
	return replay(), "", nil, errMalformedReceive
}

func validObjectID(value string) bool {
	if len(value) != 40 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

// remainingQuota is how many more bytes the repository may grow, bounded by
// both the repository and the owner's account quota.
func (a *App) remainingQuota(ctx context.Context, repo *Repository) (int64, error) {
	var total int64
	if err := a.db.QueryRow(ctx, `SELECT COALESCE(sum(size_bytes),0) FROM repositories WHERE owner_id=$1 AND deleted_at IS NULL`, repo.OwnerID).Scan(&total); err != nil {
		return 0, err
	}
	remaining := repoQuota() - repo.SizeBytes
	if account := userQuota() - total; account < remaining {
		remaining = account
	}
	if remaining < 0 {
		remaining = 0
	}
	return remaining, nil
}

// appliedUpdates keeps the updates that actually took effect. receive-pack
// succeeds even when the pre-receive hook refuses some or all refs.
func (a *App) appliedUpdates(ctx context.Context, repoID string, updates []refUpdate) []refUpdate {
	if len(updates) == 0 {
		return updates
	}
	output, err := a.git.Run(ctx, repoID, nil, "for-each-ref", "--format=%(objectname) %(refname)")
	if err != nil {
		return nil
	}
	current := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		if sha, ref, ok := strings.Cut(line, " "); ok {
			current[ref] = sha
		}
	}
	zero := strings.Repeat("0", 40)
	applied := []refUpdate{}
	for _, update := range updates {
		if (update.New == zero && current[update.Ref] == "") || (update.New != zero && current[update.Ref] == update.New) {
			applied = append(applied, update)
		}
	}
	return applied
}
