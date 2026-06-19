package main

import (
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// ---- program name ----

var progName string

func init() {
	progName = strings.TrimSuffix(filepath.Base(os.Args[0]), filepath.Ext(os.Args[0]))
}

// ---- ANSI colors ----

const (
	colorReset  = "\033[0m"
	colorDir    = "\033[1;34m" // bold blue
	colorExe    = "\033[1;32m" // bold green
	colorSymlink = "\033[1;36m" // bold cyan
	colorHidden = "\033[2;37m" // dim white
)

var useColor bool

func init() {
	// Enable color when stdout is a terminal (best-effort on Windows)
	fi, err := os.Stdout.Stat()
	if err == nil && (fi.Mode()&os.ModeCharDevice) != 0 {
		useColor = true
	}
}

// ---- flags ----

type options struct {
	long        bool
	all         bool   // -a: include . and ..
	almostAll   bool   // -A: include hidden but not . and ..
	humanSize   bool   // -h
	recursive   bool   // -R
	reverse     bool   // -r
	sortTime    bool   // -t
	sortSize    bool   // -S
	onePerLine  bool   // -1
	dirOnly     bool   // -d
	classify    bool   // -F: append /  * @ etc.
	appendSlash bool   // -p: append / to dirs
	noColor     bool   // --no-color
	inode       bool   // -i
}

// ---- entry ----

type entry struct {
	name    string
	path    string
	info    fs.FileInfo
	link    string // symlink target, if applicable
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
	// file type
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
	return strings.HasPrefix(name, ".")
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

func colorize(e entry, opts *options, s string) string {
	if opts.noColor || !useColor {
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
	return colorize(e, opts, name)
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
		hidden := isHidden(name)

		if !opts.all && !opts.almostAll && hidden {
			continue
		}

		p := filepath.Join(dir, name)

		// Resolve symlink target for display
		link := ""
		if info.Mode()&fs.ModeSymlink != 0 {
			target, err := os.Readlink(p)
			if err == nil {
				link = target
			}
			// Re-stat through symlink for accurate info
			if fi, err := os.Stat(p); err == nil {
				info = fi
			}
		}

		entries = append(entries, entry{name: name, path: p, info: info, link: link})
	}

	if opts.all {
		// Prepend . and ..
		dot, _ := os.Lstat(dir)
		dotdot, _ := os.Lstat(filepath.Dir(dir))
		if dot != nil {
			entries = append([]entry{{name: ".", path: dir, info: dot}}, entries...)
		}
		if dotdot != nil {
			entries = append([]entry{{name: "..", path: filepath.Dir(dir), info: dotdot}}, entries[1:]...)
			// re-insert dot first
			tmp := make([]entry, 0, len(entries)+1)
			tmp = append(tmp, entry{name: ".", path: dir, info: dot})
			tmp = append(tmp, entry{name: "..", path: filepath.Dir(dir), info: dotdot})
			for _, e := range entries {
				if e.name != "." && e.name != ".." {
					tmp = append(tmp, e)
				}
			}
			entries = tmp
		}
	}

	sortEntries(entries, opts)
	return entries
}

// ---- sorting ----

func sortEntries(entries []entry, opts *options) {
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		// . and .. always first
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
	// calculate column widths
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
		fmt.Printf("%s %*s %s %s\n", mode, maxSize, sizeStr, timeStr, name)
	}
}

func printColumns(entries []entry, opts *options) {
	if len(entries) == 0 {
		return
	}

	names := make([]string, len(entries))
	maxLen := 0
	for i, e := range entries {
		// strip ANSI for width calculation
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
			if col == cols-1 || (col+1)*rows+row >= len(entries) {
				fmt.Print(name)
			} else {
				fmt.Print(name + strings.Repeat(" ", pad))
			}
		}
		fmt.Println()
	}
}

func printOnePerLine(entries []entry, opts *options) {
	for _, e := range entries {
		fmt.Println(displayName(e, opts))
	}
}

func printEntries(entries []entry, opts *options) {
	switch {
	case opts.long:
		printLong(entries, opts)
	case opts.onePerLine:
		printOnePerLine(entries, opts)
	default:
		printColumns(entries, opts)
	}
}

// ---- ANSI strip (for width calculation) ----

func stripAnsi(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == '\033' && i+1 < len(s) && s[i+1] == '[' {
			i += 2
			for i < len(s) && s[i] != 'm' {
				i++
			}
			i++ // consume 'm'
		} else {
			b.WriteByte(s[i])
			i++
		}
	}
	return b.String()
}

// ---- terminal width ----

func terminalWidth() int {
	// Try $COLUMNS env var first
	if c := os.Getenv("COLUMNS"); c != "" {
		if n, err := strconv.Atoi(c); err == nil && n > 0 {
			return n
		}
	}
	return 80 // safe default
}

// ---- flag parsing ----

func parseFlags(args []string) (*options, []string) {
	opts := &options{}
	var paths []string

	i := 0
	for i < len(args) {
		arg := args[i]
		if arg == "--" {
			paths = append(paths, args[i+1:]...)
			break
		}
		if arg == "--no-color" {
			opts.noColor = true
			i++
			continue
		}
		if arg == "--color" {
			opts.noColor = false
			i++
			continue
		}
		if arg == "-h" || arg == "--help" {
			usage()
		}
		if strings.HasPrefix(arg, "-") && len(arg) > 1 && arg != "--" {
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
	return opts, paths
}

// ---- listing logic ----

func listPath(path string, opts *options, showHeader bool) {
	info, err := os.Lstat(path)
	if err != nil {
		warn("cannot access %q: %v", path, err)
		return
	}

	// -d: treat directory as a plain entry
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
		fmt.Printf("%s:\n", path)
	}

	entries := readEntries(path, opts)
	printEntries(entries, opts)

	if opts.recursive {
		for _, e := range entries {
			if e.info.IsDir() && e.name != "." && e.name != ".." {
				fmt.Println()
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
  -l          Long format: mode, size, date, name
  -a          Include entries starting with . (including . and ..)
  -A          Include entries starting with . (excluding . and ..)
  -h          Human-readable sizes with -l (e.g. 1.2M)
  -R          Recursively list subdirectories
  -r          Reverse sort order
  -t          Sort by modification time (newest first)
  -S          Sort by file size (largest first)
  -1          One entry per line
  -d          List directory itself, not its contents
  -F          Append type indicator: / dir  * exe  @ symlink
  -p          Append / to directory names
  -i          Show inode number (always 0 on Windows)
  --no-color  Disable color output
  --color     Enable color output (default when stdout is a terminal)

Examples:
  %s
  %s -la
  %s -lh /some/path
  %s -lt --no-color
  %s -R src/
`, progName, progName, progName, progName, progName, progName)
	os.Exit(0)
}

// ---- main ----

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "--help" || os.Args[1] == "-h") {
		usage()
	}

	opts, paths := parseFlags(os.Args[1:])

	// Sort paths: files first, then directories
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

	// Print files first
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
			entries = append(entries, entry{name: filepath.Base(p), path: p, info: info, link: link})
		}
		sortEntries(entries, opts)
		printEntries(entries, opts)
		if len(dirPaths) > 0 {
			fmt.Println()
		}
	}

	// Print directories
	showHeader := len(dirPaths) > 1 || len(filePaths) > 0
	for i, p := range dirPaths {
		listPath(p, opts, showHeader)
		if i < len(dirPaths)-1 {
			fmt.Println()
		}
	}
}
