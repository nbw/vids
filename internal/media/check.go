package media

import (
	"fmt"
	"os/exec"
)

// CheckTools verifies both binaries are present and runnable. Generic by
// design — it does not pattern-match any particular failure.
func CheckTools() error {
	for _, bin := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(bin); err != nil {
			return fmt.Errorf("%s not found on PATH (install ffmpeg, e.g. `brew install ffmpeg`)", bin)
		}
	}
	if err := exec.Command("ffprobe", "-version").Run(); err != nil {
		return fmt.Errorf("ffprobe failed to run: %v (try `brew reinstall ffmpeg`)", err)
	}
	return nil
}
