package gitstore

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

func validSHA(sha string) bool {
	if len(sha) != 40 {
		return false
	}
	for _, c := range sha {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func (s *Store) applyHostConfigs(ctx context.Context, id string) error {
	for _, kv := range [][2]string{
		{"http.receivepack", "true"},
		{"receive.fsckObjects", "true"},
		{"transfer.fsckObjects", "true"},
		{"receive.denyNonFastForwards", "true"},
		{"receive.denyDeletes", "true"},
		{"receive.maxInputSize", "104857600"},
		{"uploadpack.hideRefs", "refs/gitown"},
	} {
		if _, err := s.Run(ctx, id, nil, "config", kv[0], kv[1]); err != nil {
			return err
		}
	}
	return nil
}

// localTransferEnv allows a file-protocol transfer only for a destination
// path this process computed under the store root. Remote imports do not
// use it; they keep protocol.file.allow=never.
func localTransferEnv() []string {
	return []string{
		"PATH=" + os.Getenv("PATH"),
		"LANG=C.UTF-8",
		"LC_ALL=C",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=" + os.DevNull,
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ATTR_NOSYSTEM=1",
		"GIT_CONFIG_COUNT=2",
		"GIT_CONFIG_KEY_0=core.hooksPath",
		"GIT_CONFIG_VALUE_0=" + os.DevNull,
		"GIT_CONFIG_KEY_1=protocol.file.allow",
		"GIT_CONFIG_VALUE_1=always",
	}
}

// CloneLocal copies a repository this server already stores. The source id
// is an internal identifier, never a caller-supplied path.
func (s *Store) CloneLocal(ctx context.Context, destID, sourceID string) error {
	if !identifier.MatchString(destID) || !identifier.MatchString(sourceID) || destID == sourceID {
		return ErrNotFound
	}
	dest := s.Path(destID)
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, s.Binary, "clone", "--bare", "--local", "--no-hardlinks", "--", s.Path(sourceID), dest)
	cmd.Env = localTransferEnv()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		_ = os.RemoveAll(dest)
		return fmt.Errorf("clone local repository: %w (%s)", err, strings.TrimSpace(stderr.String()))
	}
	return s.applyHostConfigs(ctx, destID)
}

// CloneHTTPS imports a public https Git URL. The caller validates the URL
// before this runs. Redirects are disabled so a public host cannot bounce
// the clone at an internal address.
func (s *Store) CloneHTTPS(ctx context.Context, destID, remote string) error {
	if !identifier.MatchString(destID) || !strings.HasPrefix(remote, "https://") || strings.ContainsAny(remote, " \t\r\n") {
		return ErrNotFound
	}
	dest := s.Path(destID)
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, s.Binary, "-c", "http.followRedirects=false", "-c", "protocol.file.allow=never", "clone", "--bare", "--depth=1", "--", remote, dest)
	cmd.Env = Environment()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		_ = os.RemoveAll(dest)
		return fmt.Errorf("clone remote repository: %w (%s)", err, strings.TrimSpace(stderr.String()))
	}
	return s.applyHostConfigs(ctx, destID)
}

func (s *Store) HeadBranch(ctx context.Context, id string) (string, error) {
	out, err := s.Run(ctx, id, nil, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		return "", err
	}
	branch := strings.TrimSpace(string(out))
	if branch == "" || strings.ContainsAny(branch, " \r\n\\") || strings.Contains(branch, "..") {
		return "", ErrNotFound
	}
	return branch, nil
}

// CopyCommit copies one commit and the objects it reaches into dest so a
// cross-repository diff or merge can read them. The commit is kept on a
// hidden ref. uploadpack.hideRefs keeps that ref off public fetches.
func (s *Store) CopyCommit(ctx context.Context, destID, sourceID, sha string) error {
	if destID == sourceID {
		return nil
	}
	if !identifier.MatchString(destID) || !identifier.MatchString(sourceID) || !validSHA(sha) {
		return ErrNotFound
	}
	if _, err := s.Run(ctx, destID, nil, "cat-file", "-e", sha); err == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	rev := exec.CommandContext(ctx, s.Binary, "--git-dir="+s.Path(sourceID), "rev-list", "--objects", sha)
	rev.Env = Environment()
	var listed bytes.Buffer
	rev.Stdout = &listed
	var revErr bytes.Buffer
	rev.Stderr = &revErr
	if err := rev.Run(); err != nil {
		return fmt.Errorf("list commit objects: %w (%s)", err, strings.TrimSpace(revErr.String()))
	}
	var names bytes.Buffer
	for _, line := range strings.Split(listed.String(), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		field := strings.SplitN(line, " ", 2)[0]
		if validSHA(field) {
			names.WriteString(field)
			names.WriteByte('\n')
		}
	}
	pack := exec.CommandContext(ctx, s.Binary, "--git-dir="+s.Path(sourceID), "pack-objects", "--stdout")
	pack.Env = Environment()
	pack.Stdin = &names
	unpack := exec.CommandContext(ctx, s.Binary, "--git-dir="+s.Path(destID), "unpack-objects", "-q")
	unpack.Env = Environment()
	reader, writer := io.Pipe()
	pack.Stdout = writer
	unpack.Stdin = reader
	var packErr error
	done := make(chan struct{})
	go func() {
		packErr = pack.Run()
		_ = writer.Close()
		close(done)
	}()
	unpackErr := unpack.Run()
	<-done
	if packErr != nil {
		return fmt.Errorf("pack commit: %w", packErr)
	}
	if unpackErr != nil {
		return fmt.Errorf("unpack commit: %w", unpackErr)
	}
	_, _ = s.Run(ctx, destID, nil, "config", "uploadpack.hideRefs", "refs/gitown")
	if _, err := s.Run(ctx, destID, nil, "update-ref", "refs/gitown/pulls/"+sha, sha); err != nil {
		return err
	}
	return nil
}
