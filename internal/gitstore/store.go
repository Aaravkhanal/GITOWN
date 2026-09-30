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
var ErrConflict = errors.New("branch changed")
var identifier = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)
var commitRange = regexp.MustCompile(`^[0-9a-f]{40}(\.\.[0-9a-f]{40})?$`)

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
	store := &Store{Root: root, Binary: binary, Backend: backend, slots: make(chan struct{}, 8)}
	if err := store.installHooks(); err != nil {
		return nil, err
	}
	return store, nil
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
	return s.run(ctx, id, input, nil, args...)
}

// Command runs Git and returns its exit code. Exit codes are not errors by
// themselves; callers decide which codes are successful.
func (s *Store) Command(ctx context.Context, timeout time.Duration, id string, args ...string) ([]byte, int, error) {
	if !identifier.MatchString(id) {
		return nil, -1, ErrNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	case <-ctx.Done():
		return nil, -1, ctx.Err()
	}
	cmd := exec.CommandContext(ctx, s.Binary, append([]string{"--git-dir=" + s.Path(id)}, args...)...)
	cmd.Env = Environment()
	var stdout, stderr boundedBuffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	cmd.WaitDelay = time.Second
	err := cmd.Run()
	if err == nil {
		return stdout.Bytes(), 0, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return stdout.Bytes(), exit.ExitCode(), nil
	}
	return nil, -1, err
}

func (s *Store) Grep(ctx context.Context, id, rev, query string) (string, error) {
	if rev == "" || strings.ContainsAny(rev, "\x00\r\n ") || query == "" || strings.HasPrefix(query, "-") || strings.ContainsAny(query, "\x00\r\n") || len(query) > 80 {
		return "", ErrNotFound
	}
	out, code, err := s.Command(ctx, 20*time.Second, id, "grep", "-n", "-I", "-F", "--max-count=3", "-e", query, rev)
	if err != nil {
		return "", err
	}
	if code == 1 {
		return "", nil
	}
	if code != 0 {
		return "", fmt.Errorf("git grep: exit %d", code)
	}
	return string(out), nil
}

func (s *Store) CreateAnnotatedTag(ctx context.Context, id, name, sha, taggerName, taggerEmail, message string) error {
	if name == "" || len(name) > 80 || strings.ContainsAny(name, " \x00\r\n\\") || strings.Contains(name, "..") || strings.HasPrefix(name, "-") || len(sha) != 40 || strings.ContainsAny(taggerName, "\r\n") || strings.ContainsAny(taggerEmail, "\r\n") || len(message) > 20000 {
		return ErrNotFound
	}
	_, err := s.Run(ctx, id, strings.NewReader(message+"\n"), "-c", "user.name="+taggerName, "-c", "user.email="+taggerEmail, "tag", "-a", "-F", "-", "--end-of-options", name, sha)
	return err
}

func (s *Store) ResolveTag(ctx context.Context, id, name string) (string, error) {
	if name == "" || len(name) > 80 || strings.ContainsAny(name, " \x00\r\n\\") || strings.Contains(name, "..") || strings.HasPrefix(name, "-") {
		return "", ErrNotFound
	}
	ref := "refs/tags/" + name
	if _, err := s.Run(ctx, id, nil, "check-ref-format", ref); err != nil {
		return "", ErrNotFound
	}
	sha, err := s.Run(ctx, id, nil, "rev-parse", "--verify", "--end-of-options", ref)
	if err != nil {
		return "", ErrNotFound
	}
	return strings.TrimSpace(string(sha)), nil
}

func (s *Store) Subjects(ctx context.Context, id, spec string) ([]string, error) {
	if !commitRange.MatchString(spec) {
		return nil, ErrNotFound
	}
	out, err := s.Run(ctx, id, nil, "log", "--format=%s", "--max-count=50", "--end-of-options", spec)
	if err != nil {
		return nil, err
	}
	text := strings.TrimSpace(string(out))
	if text == "" {
		return []string{}, nil
	}
	return strings.Split(text, "\n"), nil
}

func (s *Store) Maintain(ctx context.Context, id string) error {
	_, code, err := s.Command(ctx, 60*time.Second, id, "gc", "--prune=2.weeks.ago")
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("git gc: exit %d", code)
	}
	return nil
}

func (s *Store) run(ctx context.Context, id string, input io.Reader, extraEnv []string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	cmd := exec.CommandContext(ctx, s.Binary, append([]string{"--git-dir=" + s.Path(id)}, args...)...)
	cmd.Env = append(Environment(), extraEnv...)
	cmd.Stdin = input
	var stdout, stderr boundedBuffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	cmd.WaitDelay = time.Second
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git %s: %w", args[0], err)
	}
	return stdout.Bytes(), nil
}

func validFilePath(path string) bool {
	if path == "" || len(path) > 4096 || strings.HasPrefix(path, "/") || strings.HasSuffix(path, "/") || strings.ContainsAny(path, "\x00\r\n\\") || !utf8.ValidString(path) {
		return false
	}
	for _, part := range strings.Split(path, "/") {
		if part == "" || part == "." || part == ".." || strings.EqualFold(part, ".git") {
			return false
		}
	}
	return true
}

// CommitFile creates a normal Git commit and advances the branch with a
// compare-and-swap. Concurrent pushes therefore win or return ErrConflict;
// browser edits never overwrite a branch head they did not inspect.
func (s *Store) CommitFile(ctx context.Context, id, branch, path string, content []byte, message, username, email, expectedHead string) (string, error) {
	if !validFilePath(path) || len(content) > MaxBlob || strings.TrimSpace(message) == "" {
		return "", ErrNotFound
	}
	head, err := s.Resolve(ctx, id, branch)
	if err != nil {
		return "", err
	}
	if head != expectedHead {
		return "", ErrConflict
	}
	index, err := os.CreateTemp(s.Root, "gitown-index-*")
	if err != nil {
		return "", err
	}
	indexPath := index.Name()
	if err = index.Close(); err != nil {
		return "", err
	}
	if err = os.Remove(indexPath); err != nil {
		return "", err
	}
	defer os.Remove(indexPath)
	env := []string{"GIT_INDEX_FILE=" + indexPath}
	if _, err = s.run(ctx, id, nil, env, "read-tree", head); err != nil {
		return "", err
	}
	blob, err := s.Run(ctx, id, bytes.NewReader(content), "hash-object", "-w", "--stdin")
	if err != nil {
		return "", err
	}
	entry := []byte("100644 " + strings.TrimSpace(string(blob)) + "\t" + path + "\x00")
	if _, err = s.run(ctx, id, bytes.NewReader(entry), env, "update-index", "-z", "--index-info"); err != nil {
		return "", err
	}
	tree, err := s.run(ctx, id, nil, env, "write-tree")
	if err != nil {
		return "", err
	}
	commit, err := s.Commit(ctx, id, strings.TrimSpace(string(tree)), []string{head}, message, username, email)
	if err != nil {
		return "", err
	}
	if _, err = s.Run(ctx, id, nil, "update-ref", "refs/heads/"+branch, commit, expectedHead); err != nil {
		return "", ErrConflict
	}
	return commit, nil
}

func (s *Store) DeleteFile(ctx context.Context, id, branch, path, message, username, email, expectedHead string) (string, error) {
	if !validFilePath(path) || strings.TrimSpace(message) == "" {
		return "", ErrNotFound
	}
	head, err := s.Resolve(ctx, id, branch)
	if err != nil {
		return "", err
	}
	if head != expectedHead {
		return "", ErrConflict
	}
	kind, err := s.Run(ctx, id, nil, "cat-file", "-t", head+":"+path)
	if err != nil || strings.TrimSpace(string(kind)) != "blob" {
		return "", ErrNotFound
	}
	index, err := os.CreateTemp(s.Root, "gitown-index-*")
	if err != nil {
		return "", err
	}
	indexPath := index.Name()
	if err = index.Close(); err != nil {
		return "", err
	}
	if err = os.Remove(indexPath); err != nil {
		return "", err
	}
	defer os.Remove(indexPath)
	env := []string{"GIT_INDEX_FILE=" + indexPath}
	if _, err = s.run(ctx, id, nil, env, "read-tree", head); err != nil {
		return "", err
	}
	entry := []byte("0 " + strings.Repeat("0", 40) + "\t" + path + "\x00")
	if _, err = s.run(ctx, id, bytes.NewReader(entry), env, "update-index", "-z", "--index-info"); err != nil {
		return "", err
	}
	tree, err := s.run(ctx, id, nil, env, "write-tree")
	if err != nil {
		return "", err
	}
	commit, err := s.Commit(ctx, id, strings.TrimSpace(string(tree)), []string{head}, message, username, email)
	if err != nil {
		return "", err
	}
	if _, err = s.Run(ctx, id, nil, "update-ref", "refs/heads/"+branch, commit, expectedHead); err != nil {
		return "", ErrConflict
	}
	return commit, nil
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

// Rebase replays commits that are on head and not on base. A branch that already
// contains base fast-forwards. Conflicting replay leaves the base branch unchanged.
func (s *Store) Rebase(ctx context.Context, id, base, head, username, email string) (string, error) {
	if _, err := s.Run(ctx, id, nil, "merge-base", "--is-ancestor", base, head); err == nil {
		return head, nil
	}
	mergeBase, err := s.Run(ctx, id, nil, "merge-base", base, head)
	if err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp(s.Root, "rebase-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	if _, err = s.Run(ctx, id, nil, "worktree", "add", "--detach", dir, base); err != nil {
		return "", err
	}
	defer s.Run(context.Background(), id, nil, "worktree", "remove", "--force", dir)
	cmd := exec.CommandContext(ctx, s.Binary, "-C", dir, "-c", "user.name="+username, "-c", "user.email="+email, "rebase", "--onto", base, strings.TrimSpace(string(mergeBase)), head)
	cmd.Env = Environment()
	cmd.WaitDelay = time.Second
	if err = cmd.Run(); err != nil {
		abort := exec.CommandContext(ctx, s.Binary, "-C", dir, "rebase", "--abort")
		abort.Env = Environment()
		_ = abort.Run()
		return "", fmt.Errorf("rebase conflict: %w", err)
	}
	out, err := exec.CommandContext(ctx, s.Binary, "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
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

func (s *Store) Blob(ctx context.Context, id, branch, path string) ([]byte, error) {
	if !validFilePath(path) {
		return nil, ErrNotFound
	}
	sha, err := s.Resolve(ctx, id, branch)
	if err != nil {
		return nil, err
	}
	object := sha + ":" + path
	kind, err := s.Run(ctx, id, nil, "cat-file", "-t", object)
	if err != nil || strings.TrimSpace(string(kind)) != "blob" {
		return nil, ErrNotFound
	}
	size, err := s.Run(ctx, id, nil, "cat-file", "-s", object)
	if err != nil {
		return nil, err
	}
	n, _ := strconv.Atoi(strings.TrimSpace(string(size)))
	if n > MaxOutput {
		return nil, ErrTooLarge
	}
	return s.Run(ctx, id, nil, "cat-file", "blob", object)
}

func (s *Store) Commits(ctx context.Context, id, branch string) ([]Commit, error) {
	return s.History(ctx, id, branch, 0)
}

func (s *Store) History(ctx context.Context, id, branch string, skip int) ([]Commit, error) {
	if skip < 0 || skip > 5000 {
		return nil, ErrNotFound
	}
	sha, err := s.Resolve(ctx, id, branch)
	if err != nil {
		return nil, err
	}
	out, err := s.Run(ctx, id, nil, "log", "-30", "--skip="+strconv.Itoa(skip), "--format=%H%x00%s%x00%an%x00%aI%x00", sha, "--")
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

func (s *Store) FileCommits(ctx context.Context, id, branch, path string) ([]Commit, error) {
	if !validFilePath(path) {
		return nil, ErrNotFound
	}
	sha, err := s.Resolve(ctx, id, branch)
	if err != nil {
		return nil, err
	}
	out, err := s.Run(ctx, id, nil, "log", "-30", "--follow", "--format=%H%x00%s%x00%an%x00%aI%x00", sha, "--", path)
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
