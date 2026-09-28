# GITOWN command line

The `gitown` command gives everyday Git operations short, GITOWN-specific names while keeping the repository format and network protocol fully compatible with Git.

## Build

From the GITOWN source tree:

```sh
npm run build:cli
```

The executable is written to `.tools/bin/gitown`. Add that directory to your `PATH`, or invoke the executable by its full path.

## Commands

| GITOWN command                  | Git operation        | Purpose                                                 |
| ------------------------------- | -------------------- | ------------------------------------------------------- |
| `gitown bring URL [DIRECTORY]`  | `git clone`          | Bring a repository onto this computer.                  |
| `gitown track PATH...`          | `git add`            | Select changes for the next save.                       |
| `gitown save -m "MESSAGE"`      | `git commit`         | Save tracked changes as a commit.                       |
| `gitown send [REMOTE] [BRANCH]` | `git push`           | Send commits to a GITOWN server.                        |
| `gitown sync [REMOTE] [BRANCH]` | `git pull --ff-only` | Safely sync without creating an automatic merge commit. |
| `gitown unite BRANCH`           | `git merge`          | Unite another branch into the current branch.           |
| `gitown move BRANCH`            | `git switch`         | Move to another branch.                                 |
| `gitown look`                   | `git status`         | Inspect the working tree.                               |

Run `gitown help` to see the built-in reference and `gitown version` to see the installed version.

## Example workflow

```sh
gitown bring http://localhost:8080/git/YOUR_USERNAME/YOUR_REPOSITORY.git
cd YOUR_REPOSITORY
gitown move -c feature/first-change
# Edit files in your editor.
gitown track .
gitown save -m "Add my first change"
gitown send -u origin feature/first-change
```

After the pull request is merged:

```sh
gitown move main
gitown sync origin main
```

## Compatibility escape hatch

GITOWN intentionally keeps Git underneath, so no custom repository format locks users in. Any Git operation that does not yet have a friendly GITOWN name remains available through:

```sh
gitown git <normal Git arguments>
```

For example, `gitown git log --oneline` runs `git log --oneline`. The command forwards Git's standard input, output, errors, environment, configuration, credential prompts, and exit status.
