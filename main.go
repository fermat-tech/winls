// winls is a Unix-like ls command for Windows.
//
// It lists directory contents with long format, color output, recursive
// listing, human-readable sizes, and flexible sorting — all the ls flags you
// already know, working natively on Windows (cmd.exe, PowerShell, and Windows
// Terminal).
//
// Install:
//
//	go install github.com/fermat-tech/winls@latest
//
// Usage:
//
//	winls [OPTIONS] [FILE...]
//
// See winls --help for the full option list.
package main

import (
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mattn/go-colorable"
	"github.com/mattn/go-isatty"
)

// ---- version (injected by goreleaser / go build -ldflags) ----

var version = "dev"

// ---- program name ----

var progName string

func init() {
	progName = strings.TrimSuffix(filepath.Base(os.Args[0]), filepath.Ext(os.Args[0]))
}

// ---- stdout writer ----
//
// go-colorable translates ANSI escape codes to Win32 Console API calls on
// terminals that don't natively support ANSI (e.g. old cmd.exe). On modern
// terminals and pipes it passes bytes through unchanged.

var stdout io.Writer = colorable.NewColorableStdout()

// ---- color mode ----

// colorMode is resolved once after flags and env vars are parsed.
var useColor bool

// resolveColor determines whether color output is enabled.
// Priority (highest wins):
//  1. --no-color / --color flag
//  2. NO_COLOR env var (https://no-color.org)
//  3. WINLS_COLOR=always|never|auto
//  4. auto: enabled when stdout is a real terminal
func resolveColor(flagColor, flagNoColor bool) {
	if flagNoColor {
		useColor = false
		return
	}
	if flagColor {
		useColor = true
		return
	}
	if _, set := os.LookupEnv("NO_COLOR"); set {
		useColor = false
		return
	}
	switch strings.ToLower(os.Getenv("WINLS_COLOR")) {
	case "always", "yes", "1", "true":
		useColor = true
		return
	case "never", "no", "0", "false":
		useColor = false
		return
	}
	// auto: use isatty for reliable cross-platform terminal detection
	useColor = isatty.IsTerminal(os.Stdout.Fd()) || isatty.IsCygwinTerminal(os.Stdout.Fd())
}

// ---- ANSI colors ----

const (
	colorReset   = "\033[0m"
	colorDir     = "\033[1;34m" // bold blue
	colorExe     = "\033[1;32m" // bold green
	colorSymlink = "\033[1;36m" // bold cyan
	colorHidden  = "\033[2;37m" // dim white
)

// ---- flags ----

type options struct {
	long        bool // -l
	all         bool // -a: include . and ..
	almostAll   bool // -A: include hidden but not . and ..
	humanSize   bool // -h
	recursive   bool // -R
	reverse     bool // -r
	sortTime    bool // -t
	sortSize    bool // -S
	onePerLine  bool // -1
	dirOnly     bool // -d
	classify    bool // -F: append /  * @ etc.
	appendSlash bool // -p: append / to dirs
	inode       bool // -i
}

// ---- entry ----

type entry struct {
	name string
	path string
	info fs.FileInfo
	link string // symlink target
}

// ---- helpers ----

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, progName+": "+format+"\n", args...)
	os.Exit(1)
}

func warn(format string, args ...any) {
	fmt.Fprintf(os.Stderr, progName+": "+format+"\n", args...)
}

func humanBytes(n int64) string {
	if n < 1024 {
		return strconv.FormatInt(n, 10)
	}
	units := []string{"K", "M", "G", "T", "P"}
	f := float64(n)
	for _, u := range units {
		f /= 1024
		if f < 1024 {
			if f < 10 {
				return fmt.Sprintf("%.1f%s", f, u)
			}
			return fmt.Sprintf("%.0f%s", math.Round(f), u)
		}
	}
	return fmt.Sprintf("%.0fP", f)
}

func modeString(m fs.FileMode) string {
	var b [10]byte
	switch {
	case m.IsDir():
		b[0] = 'd'
	case m&fs.ModeSymlink != 0:
		b[0] = 'l'
	default:
		b[0] = '-'
	}
	bits := []struct {
		mask fs.FileMode
		ch   byte
	}{
		{0400, 'r'}, {0200, 'w'}, {0100, 'x'},
		{0040, 'r'}, {0020, 'w'}, {0010, 'x'},
		{0004, 'r'}, {0002, 'w'}, {0001, 'x'},
	}
	for i, bit := range bits {
		if m&bit.mask != 0 {
			b[i+1] = bit.ch
		} else {
			b[i+1] = '-'
		}
	}
	return string(b[:])
}

func isHidden(name string) bool {
	return strings.HasPrefix(filepath.Base(name), ".")
}

func isExec(info fs.FileInfo) bool {
	return info.Mode()&0111 != 0
}

func indicator(e entry, opts *options) string {
	if !opts.classify && !opts.appendSlash {
		return ""
	}
	m := e.info.Mode()
	if m.IsDir() {
		return "/"
	}
	if opts.classify {
		if m&fs.ModeSymlink != 0 {
			return "@"
		}
		if isExec(e.info) {
			return "*"
		}
	}
	return ""
}

func colorize(e entry, s string) string {
	if !useColor {
		return s
	}
	m := e.info.Mode()
	switch {
	case m.IsDir():
		return colorDir + s + colorReset
	case m&fs.ModeSymlink != 0:
		return colorSymlink + s + colorReset
	case isExec(e.info):
		return colorExe + s + colorReset
	case isHidden(e.name):
		return colorHidden + s + colorReset
	}
	return s
}

func displayName(e entry, opts *options) string {
	name := e.name + indicator(e, opts)
	if e.info.Mode()&fs.ModeSymlink != 0 && e.link != "" && opts.long {
		name = name + " -> " + e.link
	}
	return colorize(e, name)
}

// ---- reading a directory ----

func readEntries(dir string, opts *options) []entry {
	f, err := os.Open(dir)
	if err != nil {
		warn("cannot open %q: %v", dir, err)
		return nil
	}
	defer f.Close()

	infos, err := f.Readdir(-1)
	if err != nil {
		warn("cannot read %q: %v", dir, err)
		return nil
	}

	var entries []entry
	for _, info := range infos {
		name := info.Name()
		if !opts.all && !opts.almostAll && isHidden(name) {
			continue
		}

		p := filepath.Join(dir, name)
		link := ""
		if info.Mode()&fs.ModeSymlink != 0 {
			link, _ = os.Readlink(p)
			if fi, err := os.Stat(p); err == nil {
				info = fi
			}
		}
		entries = append(entries, entry{name: name, path: p, info: info, link: link})
	}

	if opts.all {
		dot, _ := os.Lstat(dir)
		dotdot, _ := os.Lstat(filepath.Dir(dir))
		var tmp []entry
		if dot != nil {
			tmp = append(tmp, entry{name: ".", path: dir, info: dot})
		}
		if dotdot != nil {
			tmp = append(tmp, entry{name: "..", path: filepath.Dir(dir), info: dotdot})
		}
		tmp = append(tmp, entries...)
		entries = tmp
	}

	sortEntries(entries, opts)
	return entries
}

// ---- sorting ----

func sortEntries(entries []entry, opts *options) {
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if a.name == "." || a.name == ".." {
			return true
		}
		if b.name == "." || b.name == ".." {
			return false
		}
		var less bool
		switch {
		case opts.sortTime:
			less = a.info.ModTime().After(b.info.ModTime())
		case opts.sortSize:
			less = a.info.Size() > b.info.Size()
		default:
			less = strings.ToLower(a.name) < strings.ToLower(b.name)
		}
		if opts.reverse {
			return !less
		}
		return less
	})
}

// ---- formatting ----

func printLong(entries []entry, opts *options) {
	maxSize := 1
	for _, e := range entries {
		var s string
		if opts.humanSize {
			s = humanBytes(e.info.Size())
		} else {
			s = strconv.FormatInt(e.info.Size(), 10)
		}
		if len(s) > maxSize {
			maxSize = len(s)
		}
	}

	now := time.Now()
	for _, e := range entries {
		mode := modeString(e.info.Mode())
		var sizeStr string
		if opts.humanSize {
			sizeStr = humanBytes(e.info.Size())
		} else {
			sizeStr = strconv.FormatInt(e.info.Size(), 10)
		}
		mt := e.info.ModTime()
		var timeStr string
		if now.Year() == mt.Year() {
			timeStr = mt.Format("Jan _2 15:04")
		} else {
			timeStr = mt.Format("Jan _2  2006")
		}
		name := displayName(e, opts)
		fmt.Fprintf(stdout, "%s %*s %s %s\n", mode, maxSize, sizeStr, timeStr, name)
	}
}

func printColumns(entries []entry, opts *options) {
	if len(entries) == 0 {
		return
	}
	names := make([]string, len(entries))
	maxLen := 0
	for i, e := range entries {
		names[i] = displayName(e, opts)
		w := utf8.RuneCountInString(stripAnsi(names[i]))
		if w > maxLen {
			maxLen = w
		}
	}

	termWidth := terminalWidth()
	colWidth := maxLen + 2
	cols := termWidth / colWidth
	if cols < 1 {
		cols = 1
	}
	rows := (len(entries) + cols - 1) / cols

	for row := 0; row < rows; row++ {
		for col := 0; col < cols; col++ {
			idx := col*rows + row
			if idx >= len(entries) {
				break
			}
			name := names[idx]
			pad := colWidth - utf8.RuneCountInString(stripAnsi(name))
			isLast := col == cols-1 || (col+1)*rows+row >= len(entries)
			if isLast {
				fmt.Fprint(stdout, name)
			} else {
				fmt.Fprint(stdout, name+strings.Repeat(" ", pad))
			}
		}
		fmt.Fprintln(stdout)
	}
}

func printOnePerLine(entries []entry, opts *options) {
	for _, e := range entries {
		fmt.Fprintln(stdout, displayName(e, opts))
	}
}

func printEntries(entries []entry, opts *options) {
	switch {
	case opts.long:
		printLong(entries, opts)
	case opts.onePerLine:
		printOnePerLine(entries, opts)
	case !stdoutIsTerminal():
		// Match GNU ls: when output is not a terminal (a pipe or file), default to
		// one entry per line instead of multi-column. This keeps `ls | while read`
		// and similar pipelines correct, where columnar output would glue several
		// names onto one line.
		printOnePerLine(entries, opts)
	default:
		printColumns(entries, opts)
	}
}

// stdoutIsTerminal reports whether standard output is an interactive terminal.
func stdoutIsTerminal() bool {
	return isatty.IsTerminal(os.Stdout.Fd()) || isatty.IsCygwinTerminal(os.Stdout.Fd())
}

// ---- ANSI strip (for column width calculation) ----

func stripAnsi(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == '\033' && i+1 < len(s) && s[i+1] == '[' {
			i += 2
			for i < len(s) && s[i] != 'm' {
				i++
			}
			i++
		} else {
			b.WriteByte(s[i])
			i++
		}
	}
	return b.String()
}

// ---- terminal width ----

func terminalWidth() int {
	if c := os.Getenv("COLUMNS"); c != "" {
		if n, err := strconv.Atoi(c); err == nil && n > 0 {
			return n
		}
	}
	return 80
}

// ---- flag parsing ----

func parseFlags(args []string) (*options, []string, bool, bool) {
	opts := &options{}
	var paths []string
	var flagColor, flagNoColor bool

	i := 0
	for i < len(args) {
		arg := args[i]
		if arg == "--" {
			paths = append(paths, args[i+1:]...)
			break
		}
		switch arg {
		case "--no-color":
			flagNoColor = true
			i++
			continue
		case "--color":
			flagColor = true
			i++
			continue
		case "--version":
			fmt.Fprintf(os.Stderr, "%s %s\n", progName, version)
			os.Exit(0)
		case "-h", "--help":
			usage()
		}
		if strings.HasPrefix(arg, "-") && len(arg) > 1 {
			for _, ch := range arg[1:] {
				switch ch {
				case 'l':
					opts.long = true
				case 'a':
					opts.all = true
				case 'A':
					opts.almostAll = true
				case 'h':
					opts.humanSize = true
				case 'R':
					opts.recursive = true
				case 'r':
					opts.reverse = true
				case 't':
					opts.sortTime = true
				case 'S':
					opts.sortSize = true
				case '1':
					opts.onePerLine = true
				case 'd':
					opts.dirOnly = true
				case 'F':
					opts.classify = true
				case 'p':
					opts.appendSlash = true
				case 'i':
					opts.inode = true
				default:
					fatal("unknown option -%c", ch)
				}
			}
			i++
			continue
		}
		paths = append(paths, arg)
		i++
	}

	if len(paths) == 0 {
		paths = []string{"."}
	}
	return opts, paths, flagColor, flagNoColor
}

// ---- listing logic ----

func listPath(path string, opts *options, showHeader bool) {
	info, err := os.Lstat(path)
	if err != nil {
		warn("cannot access %q: %v", path, err)
		return
	}

	if opts.dirOnly || !info.IsDir() {
		link := ""
		if info.Mode()&fs.ModeSymlink != 0 {
			link, _ = os.Readlink(path)
		}
		e := entry{name: filepath.Base(path), path: path, info: info, link: link}
		printEntries([]entry{e}, opts)
		return
	}

	if showHeader {
		fmt.Fprintf(stdout, "%s:\n", path)
	}

	entries := readEntries(path, opts)
	printEntries(entries, opts)

	if opts.recursive {
		for _, e := range entries {
			if e.info.IsDir() && e.name != "." && e.name != ".." {
				fmt.Fprintln(stdout)
				listPath(e.path, opts, true)
			}
		}
	}
}

// ---- usage ----

func usage() {
	fmt.Fprintf(os.Stderr, `Usage: %s [OPTIONS] [FILE...]

List directory contents.

Options:
  -l            Long format: mode, size, date, name
  -a            Include entries starting with . (including . and ..)
  -A            Include entries starting with . (excluding . and ..)
  -h            Human-readable sizes with -l (e.g. 1.2M)
  -R            Recursively list subdirectories
  -r            Reverse sort order
  -t            Sort by modification time (newest first)
  -S            Sort by file size (largest first)
  -1            One entry per line
  -d            List directory itself, not its contents
  -F            Append type indicator: / dir  * exe  @ symlink
  -p            Append / to directory names
  -i            Show inode number (always 0 on Windows)
  --color       Force color output on
  --no-color    Force color output off
  --version     Print version and exit

Color control (lowest to highest priority):
  Auto          Enabled when stdout is a terminal, disabled when piped
  WINLS_COLOR   Set to always, never, or auto
  NO_COLOR      Set to any value to disable color (https://no-color.org)
  --color       Force enable
  --no-color    Force disable

Examples:
  %s
  %s -la
  %s -lh /some/path
  %s -lt --no-color
  %s -R src/
`, progName, progName, progName, progName, progName, progName)
	os.Exit(0)
}

// ---- glob expansion ----

// expandGlobs expands any path argument that contains glob metacharacters.
// On Windows the shell does not expand globs, so we do it here.
// Non-glob paths are passed through unchanged (including paths that don't exist,
// so the normal "cannot access" error is preserved).
func expandGlobs(paths []string) []string {
	var out []string
	for _, p := range paths {
		if !strings.ContainsAny(p, "*?[") {
			out = append(out, p)
			continue
		}
		matches, err := filepath.Glob(p)
		if err != nil || len(matches) == 0 {
			// Treat an unmatched glob like a literal path so the caller can warn.
			out = append(out, p)
			continue
		}
		out = append(out, matches...)
	}
	return out
}

// ---- main ----

func main() {
	opts, paths, flagColor, flagNoColor := parseFlags(os.Args[1:])
	resolveColor(flagColor, flagNoColor)
	paths = expandGlobs(paths)

	var filePaths, dirPaths []string
	for _, p := range paths {
		info, err := os.Lstat(p)
		if err != nil {
			warn("cannot access %q: %v", p, err)
			continue
		}
		if info.IsDir() && !opts.dirOnly {
			dirPaths = append(dirPaths, p)
		} else {
			filePaths = append(filePaths, p)
		}
	}

	if len(filePaths) > 0 {
		var entries []entry
		for _, p := range filePaths {
			info, err := os.Lstat(p)
			if err != nil {
				warn("cannot access %q: %v", p, err)
				continue
			}
			link := ""
			if info.Mode()&fs.ModeSymlink != 0 {
				link, _ = os.Readlink(p)
			}
			entries = append(entries, entry{name: p, path: p, info: info, link: link})
		}
		sortEntries(entries, opts)
		printEntries(entries, opts)
		if len(dirPaths) > 0 {
			fmt.Fprintln(stdout)
		}
	}

	showHeader := len(dirPaths) > 1 || len(filePaths) > 0
	for i, p := range dirPaths {
		listPath(p, opts, showHeader)
		if i < len(dirPaths)-1 {
			fmt.Fprintln(stdout)
		}
	}
}
