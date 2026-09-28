// Package gitstore is the only application package that starts Git subprocesses.
package gitstore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const MaxOutput = 4 << 20
const MaxBlob = 512 << 10

var ErrTooLarge = errors.New("Git output exceeds display limit")
var ErrNotFound = errors.New("ref or path does not exist")
var identifier = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)

type Store struct {
	Root, Binary, Backend string
	slots                 chan struct{}
}
type Entry struct {
	Name string `json:"name"`
	Type string `json:"type"`
	SHA  string `json:"sha"`
}
type Commit struct {
	SHA     string `json:"sha"`
	Message string `json:"message"`
	Author  string `json:"author"`
	Date    string `json:"date"`
}
type Tree struct {
	Branch  string  `json:"branch"`
	Path    string  `json:"path"`
	SHA     string  `json:"sha"`
	Entries []Entry `json:"entries"`
	Content *string `json:"content,omitempty"`
	Binary  bool    `json:"binary"`
}

func New(root string) (*Store, error) {
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	binary, err := exec.LookPath("git")
	if err != nil {
		return nil, errors.New("Git must be installed")
	}
	out, err := exec.Command(binary, "--exec-path").Output()
	if err != nil {
		return nil, err
	}
	backend := filepath.Join(strings.TrimSpace(string(out)), "git-http-backend")
	if _, err := os.Stat(backend); err != nil {
		return nil, err
	}
	return &Store{Root: root, Binary: binary, Backend: backend, slots: make(chan struct{}, 8)}, nil
}

func (s *Store) Path(id string) string {
	if !identifier.MatchString(id) {
		panic("invalid internal repository id")
	}
	return filepath.Join(s.Root, id+".git")
}

func Environment() []string {
	return []string{"PATH=" + os.Getenv("PATH"), "LANG=C.UTF-8", "LC_ALL=C", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_TERMINAL_PROMPT=0", "GIT_ATTR_NOSYSTEM=1", "GIT_CONFIG_COUNT=2", "GIT_CONFIG_KEY_0=core.hooksPath", "GIT_CONFIG_VALUE_0=" + os.DevNull, "GIT_CONFIG_KEY_1=protocol.file.allow", "GIT_CONFIG_VALUE_1=never"}
}

type boundedBuffer struct{ bytes.Buffer }

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > MaxOutput {
		return 0, ErrTooLarge
	}
	return b.Buffer.Write(p)
}

func (s *Store) Run(ctx context.Context, id string, input io.Reader, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	cmd := exec.CommandContext(ctx, s.Binary, append([]string{"--git-dir=" + s.Path(id)}, args...)...)
	cmd.Env = Environment()
	cmd.Stdin = input
	var stdout, stderr boundedBuffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	cmd.WaitDelay = time.Second
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git %s: %w", args[0], err)
	}
	return stdout.Bytes(), nil
}

func (s *Store) Init(ctx context.Context, id, name, username, email string, readme bool) error {
	dir := s.Path(id)
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	// Exclusive creation prevents accidental reuse of an existing repository.
	if err := os.Mkdir(dir, 0700); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, s.Binary, "init", "--bare", "--initial-branch=main", "--", dir)
	cmd.Env = Environment()
	if err := cmd.Run(); err != nil {
		return err
	}
	for _, kv := range [][2]string{{"http.receivepack", "true"}, {"receive.fsckObjects", "true"}, {"transfer.fsckObjects", "true"}, {"receive.denyNonFastForwards", "true"}, {"receive.denyDeletes", "true"}, {"receive.maxInputSize", "104857600"}} {
		if _, err := s.Run(ctx, id, nil, "config", kv[0], kv[1]); err != nil {
			return err
		}
	}
	if !readme {
		return nil
	}
	blob, err := s.Run(ctx, id, strings.NewReader("# "+name+"\n\nWelcome to your new repository on GITOWN.\n"), "hash-object", "-w", "--stdin")
	if err != nil {
		return err
	}
	tree, err := s.Run(ctx, id, strings.NewReader("100644 blob "+strings.TrimSpace(string(blob))+"\tREADME.md\n"), "mktree")
	if err != nil {
		return err
	}
	sha, err := s.Commit(ctx, id, strings.TrimSpace(string(tree)), nil, "Initial commit", username, email)
	if err != nil {
		return err
	}
	_, err = s.Run(ctx, id, nil, "update-ref", "refs/heads/main", sha, strings.Repeat("0", 40))
	return err
}

func (s *Store) Commit(ctx context.Context, id, tree string, parents []string, message, username, email string) (string, error) {
	args := []string{"-c", "user.name=" + username, "-c", "user.email=" + email, "commit-tree", tree}
	for _, p := range parents {
		args = append(args, "-p", p)
	}
	out, err := s.Run(ctx, id, strings.NewReader(message+"\n"), args...)
	return strings.TrimSpace(string(out)), err
}

func (s *Store) Branches(ctx context.Context, id string) ([]string, error) {
	out, err := s.Run(ctx, id, nil, "for-each-ref", "--format=%(refname:strip=2)", "refs/heads/")
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(out)) == 0 {
		return []string{}, nil
	}
	return strings.Split(strings.TrimSpace(string(out)), "\n"), nil
}

func (s *Store) Resolve(ctx context.Context, id, branch string) (string, error) {
	if len(branch) == 0 || len(branch) > 255 || strings.ContainsAny(branch, "\x00\r\n") {
		return "", ErrNotFound
	}
	ref := "refs/heads/" + branch
	if _, err := s.Run(ctx, id, nil, "check-ref-format", ref); err != nil {
		return "", ErrNotFound
	}
	sha, err := s.Run(ctx, id, nil, "rev-parse", "--verify", "--end-of-options", ref+"^{commit}")
	if err != nil {
		return "", ErrNotFound
	}
	return strings.TrimSpace(string(sha)), nil
}

func (s *Store) Browse(ctx context.Context, id, branch, path string) (Tree, error) {
	t := Tree{Branch: branch, Path: path, Entries: []Entry{}}
	sha, err := s.Resolve(ctx, id, branch)
	if err != nil {
		return t, err
	}
	t.SHA = sha
	if len(path) > 4096 || strings.ContainsAny(path, "\x00\r\n") {
		return t, ErrNotFound
	}
	for _, p := range strings.Split(path, "/") {
		if p == ".." || p == "." {
			return t, ErrNotFound
		}
	}
	object := sha + ":" + path
	kind, err := s.Run(ctx, id, nil, "cat-file", "-t", object)
	if err != nil {
		return t, ErrNotFound
	}
	switch strings.TrimSpace(string(kind)) {
	case "tree":
		out, err := s.Run(ctx, id, nil, "ls-tree", "-z", object)
		if err != nil {
			return t, err
		}
		for _, line := range bytes.Split(out, []byte{0}) {
			if len(line) == 0 {
				continue
			}
			head, name, ok := strings.Cut(string(line), "\t")
			fields := strings.Fields(head)
			if !ok || len(fields) != 3 {
				return t, errors.New("invalid tree")
			}
			t.Entries = append(t.Entries, Entry{Name: name, Type: fields[1], SHA: fields[2]})
		}
	case "blob":
		size, err := s.Run(ctx, id, nil, "cat-file", "-s", object)
		if err != nil {
			return t, err
		}
		n, _ := strconv.Atoi(strings.TrimSpace(string(size)))
		if n > MaxBlob {
			return t, ErrTooLarge
		}
		out, err := s.Run(ctx, id, nil, "cat-file", "blob", object)
		if err != nil {
			return t, err
		}
		t.Binary = bytes.Contains(out, []byte{0}) || !utf8.Valid(out)
		if !t.Binary {
			content := string(out)
			t.Content = &content
		}
	default:
		return t, ErrNotFound
	}
	return t, nil
}

func (s *Store) Commits(ctx context.Context, id, branch string) ([]Commit, error) {
	sha, err := s.Resolve(ctx, id, branch)
	if err != nil {
		return nil, err
	}
	out, err := s.Run(ctx, id, nil, "log", "-30", "--format=%H%x00%s%x00%an%x00%aI%x00", sha, "--")
	if err != nil {
		return nil, err
	}
	parts := strings.Split(string(out), "\x00")
	commits := []Commit{}
	for i := 0; i+3 < len(parts); i += 4 {
		commits = append(commits, Commit{SHA: strings.TrimSpace(parts[i]), Message: parts[i+1], Author: parts[i+2], Date: parts[i+3]})
	}
	return commits, nil
}
