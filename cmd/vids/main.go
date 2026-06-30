// Command vids is an interactive terminal tool for resizing video files,
// built on top of ffmpeg/ffprobe.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nbw/vids/internal/media"
	"github.com/nbw/vids/internal/tui"
)

// version is set at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.BoolVar(showVersion, "v", false, "print version and exit (shorthand)")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "vids — interactive video resizer\n\nUsage:\n  vids [path]\n\nFlags:\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	if *showVersion {
		fmt.Printf("vids %s\n", version)
		return
	}

	dir := "."
	if args := flag.Args(); len(args) > 0 {
		dir = args[0]
	}

	if err := media.CheckTools(); err != nil {
		fatal(err.Error())
	}

	absDir, err := filepath.Abs(dir)
	if err != nil {
		fatal(err.Error())
	}
	info, err := os.Stat(absDir)
	if err != nil || !info.IsDir() {
		fatal(fmt.Sprintf("not a directory: %s", dir))
	}

	files, err := media.ListVideos(absDir)
	if err != nil {
		fatal(err.Error())
	}

	p := tea.NewProgram(tui.New(absDir, files), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fatal(err.Error())
	}
}

func fatal(msg string) {
	fmt.Fprintln(os.Stderr, "vids: "+msg)
	os.Exit(1)
}
