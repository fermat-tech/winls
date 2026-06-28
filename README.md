# winls

A Unix-like `ls` command for Windows, written in Go.

List directory contents with long format, human-readable sizes, color output, recursive listing, and flexible sorting — all the `ls` flags you already know, working natively on Windows.

Rename the binary to anything you like (`ll`, `dir2`, etc.) — all usage and error messages derive from the executable name automatically.

## Installation

### go install (recommended)

Requires [Go](https://golang.org) 1.21+.

```powershell
go install github.com/fermat-tech/winls@latest
```

The binary lands in `%USERPROFILE%\go\bin`, which should already be on your `PATH`.

### Build from source

```powershell
git clone https://github.com/fermat-tech/winls.git
cd winls
go build -ldflags "-X main.version=v1.4.0" -o winls.exe .
```

Omit the `-ldflags` part for a quick local build; `winls --version` will report `dev`.

### Download a pre-built binary

Grab the latest binary from [Releases](https://github.com/fermat-tech/winls/releases), or download directly with PowerShell:

```powershell
# Replace v1.4.0 with the latest tag shown on the Releases page
$ver = "v1.4.0"
Invoke-WebRequest "https://github.com/fermat-tech/winls/releases/download/$ver/winls.exe" -OutFile winls.exe
```

## Usage

```
winls [OPTIONS] [FILE...]
```

With no arguments, lists the current directory.

## Options

| Flag | Description |
|------|-------------|
| `-l` | Long format: mode, size, date, name |
| `-a` | Include hidden entries (starting with `.`), including `.` and `..` |
| `-A` | Include hidden entries, excluding `.` and `..` |
| `-h` | Human-readable sizes with `-l` (e.g. `1.2M`, `340K`) |
| `-R` | Recursively list subdirectories |
| `-r` | Reverse sort order |
| `-t` | Sort by modification time (newest first) |
| `-S` | Sort by file size (largest first) |
| `-1` | One entry per line |
| `-d` | List directory itself, not its contents |
| `-F` | Append type indicator: `/` dir, `*` executable, `@` symlink |
| `-p` | Append `/` to directory names |
| `-i` | Show inode number (always `0` on Windows) |
| `--color` | Force color output on |
| `--no-color` | Force color output off |
| `--version` | Print version and exit |

Flags can be combined: `-lhA`, `-lt`, `-lSr`, etc.

## Color output

Colors work on all Windows terminals including the old Command Prompt (cmd.exe), PowerShell, and Windows Terminal.

| Color | Meaning |
|-------|---------|
| Bold blue | Directory |
| Bold green | Executable |
| Bold cyan | Symbolic link |
| Dim white | Hidden file |

Color is controlled by the following, in order of priority (highest wins):

| Method | Example |
|--------|---------|
| `--no-color` flag | `winls --no-color` |
| `--color` flag | `winls --color` |
| `NO_COLOR` env var | `set NO_COLOR=1` — disables color ([no-color.org](https://no-color.org)) |
| `WINLS_COLOR` env var | `set WINLS_COLOR=always` / `never` / `auto` |
| Auto (default) | Enabled when stdout is a terminal, disabled when piped |

## Examples

```powershell
# List current directory
winls

# Long format with human-readable sizes
winls -lh

# Show all files including hidden, long format
winls -la

# Sort by size, largest first
winls -lS

# Sort by modification time, newest first
winls -lt

# Recursive listing
winls -R src/

# List specific path with type indicators
winls -F C:\Users

# Combine flags freely
winls -lhAtr
```

## License

MIT
