package gitstore

import (
	"os"
	"path/filepath"
	"strings"
)

// preReceiveHook enforces branch rules after a push's objects arrive in
// quarantine but before any ref moves. The server passes this push's rules in
// GITOWN_* environment variables as space-separated full ref names; Git ref
// names cannot contain spaces or glob characters, so the case patterns below
// match exact refs.
const preReceiveHook = `#!/bin/sh
zero=0000000000000000000000000000000000000000
status=0
if [ -n "$GITOWN_QUOTA_REMAINING" ] && [ -n "$GIT_QUARANTINE_PATH" ] && [ -d "$GIT_QUARANTINE_PATH" ]; then
	incoming=$(du -sk "$GIT_QUARANTINE_PATH" | cut -f1)
	if [ $((incoming * 1024)) -gt "$GITOWN_QUOTA_REMAINING" ]; then
		echo "GITOWN: repository or account storage quota would be exceeded." >&2
		exit 1
	fi
fi
while read -r old new ref; do
	case " $GITOWN_BLOCK_UNITE " in *" $ref "*)
		echo "GITOWN: $ref requires a Unite request; direct pushes are disabled." >&2
		status=1
		continue
		;;
	esac
	case " $GITOWN_BLOCK_RESTRICTED " in *" $ref "*)
		echo "GITOWN: only the owner or a maintainer can push to $ref." >&2
		status=1
		continue
		;;
	esac
	if [ "$new" = "$zero" ]; then
		case " $GITOWN_NO_DELETE " in *" $ref "*)
			echo "GITOWN: $ref is protected and cannot be deleted." >&2
			status=1
			;;
		esac
		continue
	fi
	if [ "$old" != "$zero" ]; then
		case " $GITOWN_NO_FORCE " in *" $ref "*)
			if ! git merge-base --is-ancestor "$old" "$new"; then
				echo "GITOWN: force pushes to $ref are not allowed." >&2
				status=1
				continue
			fi
			;;
		esac
	fi
	case " $GITOWN_SIGNED " in *" $ref "*)
		if [ "$old" = "$zero" ]; then
			marks=$(git -c gpg.ssh.allowedSignersFile="${GITOWN_ALLOWED_SIGNERS:-/dev/null}" log --format=%G? "$new" --not --all) || marks=N
		else
			marks=$(git -c gpg.ssh.allowedSignersFile="${GITOWN_ALLOWED_SIGNERS:-/dev/null}" log --format=%G? "$old..$new") || marks=N
		fi
		for mark in $marks; do
			if [ "$mark" != G ]; then
				echo "GITOWN: every commit pushed to $ref needs a verified signature from a registered signing key." >&2
				status=1
				break
			fi
		done
		;;
	esac
done
exit $status
`

// HooksPath is the directory holding GITOWN's receive hooks.
func (s *Store) HooksPath() string {
	return filepath.Join(s.Root, "_gitown", "hooks")
}

func (s *Store) installHooks() error {
	dir := s.HooksPath()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	path := filepath.Join(dir, "pre-receive")
	if current, err := os.ReadFile(path); err == nil && string(current) == preReceiveHook {
		return nil
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, []byte(preReceiveHook), 0700); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

// ReceiveEnvironment is the environment for git receive-pack. It enables the
// pre-receive hook and lets that hook, rather than repository-wide settings,
// decide whether force pushes and deletions are allowed.
func (s *Store) ReceiveEnvironment() []string {
	env := []string{}
	for _, entry := range Environment() {
		if !strings.HasPrefix(entry, "GIT_CONFIG_") {
			env = append(env, entry)
		}
	}
	return append(env,
		"GIT_CONFIG_COUNT=4",
		"GIT_CONFIG_KEY_0=core.hooksPath", "GIT_CONFIG_VALUE_0="+s.HooksPath(),
		"GIT_CONFIG_KEY_1=protocol.file.allow", "GIT_CONFIG_VALUE_1=never",
		"GIT_CONFIG_KEY_2=receive.denyNonFastForwards", "GIT_CONFIG_VALUE_2=false",
		"GIT_CONFIG_KEY_3=receive.denyDeletes", "GIT_CONFIG_VALUE_3=false",
	)
}
